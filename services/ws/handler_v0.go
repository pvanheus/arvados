package main

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"sync"
	"time"
)

var (
	errQueueFull   = errors.New("client queue full")
	errFrameTooBig = errors.New("frame too big")
)

type handlerV0 struct {
	PingTimeout time.Duration
	QueueSize   int
}

func (h *handlerV0) debugLogf(ws wsConn, s string, args ...interface{}) {
	args = append([]interface{}{ws.Request().RemoteAddr}, args...)
	debugLogf("%s "+s, args...)
}

func (h *handlerV0) Handle(ws wsConn, events <-chan *event) {
	queue := make(chan *event, h.QueueSize)
	mtx := sync.Mutex{}
	subscribed := make(map[string]bool)

	stopped := make(chan struct{})
	stop := make(chan error, 5)

	go func() {
		buf := make([]byte, 2<<20)
		for {
			select {
			case <-stopped:
				return
			default:
			}
			ws.SetReadDeadline(time.Now().Add(h.PingTimeout))
			n, err := ws.Read(buf)
			h.debugLogf(ws, "received frame: %q", buf[:n])
			if err == nil && n == len(buf) {
				err = errFrameTooBig
			}
			if err, ok := err.(timeouter); ok && err.Timeout() {
				// If the outgoing queue is empty,
				// send an empty message. This can
				// help detect a disconnected network
				// socket, and prevent an idle socket
				// from being closed.
				if len(queue) == 0 {
					queue <- nil
				}
				continue
			}
			if err != nil {
				if err != io.EOF {
					h.debugLogf(ws, "handlerV0: read: %s", err)
				}
				stop <- err
				return
			}
			msg := make(map[string]interface{})
			err = json.Unmarshal(buf[:n], &msg)
			if err != nil {
				h.debugLogf(ws, "handlerV0: unmarshal: %s", err)
				stop <- err
				return
			}
			h.debugLogf(ws, "received message: %+v", msg)
			h.debugLogf(ws, "subscribing to *")
			subscribed["*"] = true
		}
	}()

	go func() {
		for e := range queue {
			if e == nil {
				_, err := ws.Write([]byte("{}\n"))
				if err != nil {
					h.debugLogf(ws, "handlerV0: write: %s", err)
					stop <- err
					break
				}
				continue
			}
			detail := e.Detail()
			if detail == nil {
				continue
			}
			// FIXME: check permission
			buf, err := json.Marshal(map[string]interface{}{
				"msgID":             e.Serial,
				"id":                detail.ID,
				"uuid":              detail.UUID,
				"object_uuid":       detail.ObjectUUID,
				"object_owner_uuid": detail.ObjectOwnerUUID,
				"event_type":        detail.EventType,
			})
			if err != nil {
				log.Printf("error encoding: ", err)
				continue
			}
			_, err = ws.Write(append(buf, byte('\n')))
			if err != nil {
				h.debugLogf(ws, "handlerV0: write: %s", err)
				stop <- err
				break
			}
		}
		for _ = range queue {}
	}()

	// Filter incoming events against the current subscription
	// list, and forward matching events to the outgoing message
	// queue. Close the queue and return when the "stopped"
	// channel closes or the incoming event stream ends. Shut down
	// the handler if the outgoing queue fills up.
	go func() {
		send := func(e *event) {
			select {
			case queue <- e:
			default:
				stop <- errQueueFull
			}
		}

		for {
			var e *event
			var ok bool
			select {
			case <-stopped:
				close(queue)
				return
			case e, ok = <-events:
				if !ok {
					close(queue)
					return
				}
			}
			detail := e.Detail()
			mtx.Lock()
			switch {
			case subscribed["*"]:
				send(e)
			case detail == nil:
			case subscribed[detail.ObjectUUID]:
				send(e)
			case subscribed[detail.ObjectOwnerUUID]:
				send(e)
			}
			mtx.Unlock()
		}
	}()

	<-stop
	close(stopped)
}
