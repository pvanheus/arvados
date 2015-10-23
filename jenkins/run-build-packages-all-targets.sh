#!/bin/bash

read -rd "\000" helpmessage <<EOF
$(basename $0): Orchestrate run-build-packages.sh for every target

Syntax:
        WORKSPACE=/path/to/arvados $(basename $0) [options]

Options:

--command
    Build command to execute (default: use built-in Docker image command)
--test-packages
    Run package install tests
--debug
    Output debug information (default: false)

WORKSPACE=path         Path to the Arvados source tree to build packages from

EOF

if ! [[ -n "$WORKSPACE" ]]; then
  echo >&2 "$helpmessage"
  echo >&2
  echo >&2 "Error: WORKSPACE environment variable not set"
  echo >&2
  exit 1
fi

if ! [[ -d "$WORKSPACE" ]]; then
  echo >&2 "$helpmessage"
  echo >&2
  echo >&2 "Error: $WORKSPACE is not a directory"
  echo >&2
  exit 1
fi

set -e

PARSEDOPTS=$(getopt --name "$0" --longoptions \
    help,test-packages,debug,command: \
    -- "" "$@")
if [ $? -ne 0 ]; then
    exit 1
fi

COMMAND=
DEBUG=
TEST_PACKAGES=

eval set -- "$PARSEDOPTS"
while [ $# -gt 0 ]; do
    case "$1" in
        --help)
            echo >&2 "$helpmessage"
            echo >&2
            exit 1
            ;;
        --debug)
            DEBUG="--debug"
            ;;
        --command)
            COMMAND="$2"; shift
            ;;
        --test-packages)
            TEST_PACKAGES="--test-packages"
            ;;
        --)
            if [ $# -gt 1 ]; then
                echo >&2 "$0: unrecognized argument '$2'. Try: $0 --help"
                exit 1
            fi
            ;;
    esac
    shift
done

for dockerfile_path in $(find -name Dockerfile); do
    ./run-build-packages-one-target.sh --target "$(basename $(dirname "$dockerfile_path"))" --command "$COMMAND" $DEBUG $TEST_PACKAGES
done
