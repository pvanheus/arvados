package main

import (
	"git.curoverse.com/arvados.git/sdk/go/arvadosclient"
	"git.curoverse.com/arvados.git/sdk/go/arvadostest"

	"bytes"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	. "gopkg.in/check.v1"
)

// Gocheck boilerplate
func Test(t *testing.T) {
	TestingT(t)
}

var _ = Suite(&TestSuite{})
var _ = Suite(&MockArvadosServerSuite{})

type TestSuite struct{}
type MockArvadosServerSuite struct{}

var initialArgs []string

func (s *TestSuite) SetUpSuite(c *C) {
	initialArgs = os.Args
	arvadostest.StartAPI()
}

func (s *TestSuite) TearDownSuite(c *C) {
	arvadostest.StopAPI()
}

func (s *TestSuite) SetUpTest(c *C) {
	args := []string{"crunch-dispatch-slurm"}
	os.Args = args

	var err error
	arv, err = arvadosclient.MakeArvadosClient()
	if err != nil {
		c.Fatalf("Error making arvados client: %s", err)
	}
	os.Setenv("ARVADOS_API_TOKEN", arvadostest.Dispatch1Token)
}

func (s *TestSuite) TearDownTest(c *C) {
	arvadostest.ResetEnv()
	os.Args = initialArgs
}

func (s *MockArvadosServerSuite) TearDownTest(c *C) {
	arvadostest.ResetEnv()
}

func (s *TestSuite) Test_doMain(c *C) {
	args := []string{"-poll-interval", "2", "-container-priority-poll-interval", "1", "-crunch-run-command", "echo"}
	os.Args = append(os.Args, args...)

	var sbatchCmdLine []string
	var striggerCmdLine []string

	// Override sbatchCmd
	defer func(orig func(Container) *exec.Cmd) {
		sbatchCmd = orig
	}(sbatchCmd)
	sbatchCmd = func(container Container) *exec.Cmd {
		sbatchCmdLine = sbatchFunc(container).Args
		return exec.Command("sh")
	}

	// Override striggerCmd
	defer func(orig func(jobid, containerUUID, finishCommand,
		apiHost, apiToken, apiInsecure string) *exec.Cmd) {
		striggerCmd = orig
	}(striggerCmd)
	striggerCmd = func(jobid, containerUUID, finishCommand, apiHost, apiToken, apiInsecure string) *exec.Cmd {
		striggerCmdLine = striggerFunc(jobid, containerUUID, finishCommand,
			apiHost, apiToken, apiInsecure).Args
		go func() {
			time.Sleep(5 * time.Second)
			for _, state := range []string{"Running", "Complete"} {
				arv.Update("containers", containerUUID,
					arvadosclient.Dict{
						"container": arvadosclient.Dict{"state": state}},
					nil)
			}
		}()
		return exec.Command("echo", "strigger")
	}

	go func() {
		time.Sleep(8 * time.Second)
		sigChan <- syscall.SIGINT
	}()

	// There should be no queued containers now
	params := arvadosclient.Dict{
		"filters": [][]string{[]string{"state", "=", "Queued"}},
	}
	var containers ContainerList
	err := arv.List("containers", params, &containers)
	c.Check(err, IsNil)
	c.Check(len(containers.Items), Equals, 1)

	err = doMain()
	c.Check(err, IsNil)

	item := containers.Items[0]
	sbatchCmdComps := []string{"sbatch", "--share", "--parsable",
		fmt.Sprintf("--job-name=%s", item.UUID),
		fmt.Sprintf("--mem-per-cpu=%s", strconv.Itoa(int(math.Ceil(float64(item.RuntimeConstraints["ram"])/float64(item.RuntimeConstraints["vcpus"]*1048576))))),
		fmt.Sprintf("--cpus-per-task=%s", strconv.Itoa(int(item.RuntimeConstraints["vcpus"])))}
	c.Check(sbatchCmdLine, DeepEquals, sbatchCmdComps)

	c.Check(striggerCmdLine, DeepEquals, []string{"strigger", "--set", "--jobid=zzzzz-dz642-queuedcontainer\n", "--fini",
		"--program=/usr/bin/crunch-finish-slurm.sh " + os.Getenv("ARVADOS_API_HOST") + " " + arvadostest.Dispatch1Token + " 1 zzzzz-dz642-queuedcontainer"})

	// There should be no queued containers now
	err = arv.List("containers", params, &containers)
	c.Check(err, IsNil)
	c.Check(len(containers.Items), Equals, 0)

	// Previously "Queued" container should now be in "Complete" state
	var container Container
	err = arv.Get("containers", "zzzzz-dz642-queuedcontainer", nil, &container)
	c.Check(err, IsNil)
	c.Check(container.State, Equals, "Complete")
}

func (s *MockArvadosServerSuite) Test_APIErrorGettingContainers(c *C) {
	apiStubResponses := make(map[string]arvadostest.StubResponse)
	apiStubResponses["/arvados/v1/api_client_authorizations/current"] = arvadostest.StubResponse{200, `{"uuid":"` + arvadostest.Dispatch1AuthUUID + `"}`}
	apiStubResponses["/arvados/v1/containers"] = arvadostest.StubResponse{500, string(`{}`)}

	testWithServerStub(c, apiStubResponses, "echo", "Error getting list of queued containers")
}

func testWithServerStub(c *C, apiStubResponses map[string]arvadostest.StubResponse, crunchCmd string, expected string) {
	apiStub := arvadostest.ServerStub{apiStubResponses}

	api := httptest.NewServer(&apiStub)
	defer api.Close()

	arv = arvadosclient.ArvadosClient{
		Scheme:    "http",
		ApiServer: api.URL[7:],
		ApiToken:  "abc123",
		Client:    &http.Client{Transport: &http.Transport{}},
		Retries:   0,
	}

	buf := bytes.NewBuffer(nil)
	log.SetOutput(buf)
	defer log.SetOutput(os.Stderr)

	go func() {
		for i := 0; i < 80 && !strings.Contains(buf.String(), expected); i++ {
			time.Sleep(100 * time.Millisecond)
		}
		sigChan <- syscall.SIGTERM
	}()

	runQueuedContainers(2, 1, crunchCmd, crunchCmd)

	c.Check(buf.String(), Matches, `(?ms).*`+expected+`.*`)
}