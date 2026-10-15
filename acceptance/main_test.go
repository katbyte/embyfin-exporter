//go:build acceptance

// Package acceptance_test runs embyfin-exporter against a real Emby or Jellyfin in Docker that holds a small made-up catalogue, and checks that the
// page it writes says what is there: so many films in the film library, one session playing while a device plays. The unit tests answer in a
// server's place with what the code expects of one; these are where a real server gets to answer otherwise.
//
// Every test is written once and runs on both servers. Where the two differ, the test says what the page holds on each and why.
//
// Two of the tests use docker themselves: one freezes the server's container to see it reported as down, and one runs the program's own image
// beside it. make testacc builds that image and names it; driven by hand, that test skips unless EMBYFIN_EXPORTER_TEST_PROGRAM_IMAGE names one.
//
//	make testacc                  # both servers, each started and torn down
//	make testacc-jellyfin         # one of them
//
//	eval "$(EMBYFIN_EXPORTER_TEST_BACKEND=jellyfin .scripts/testenv.sh up)"  # or drive it by hand
//	make test-live
//	EMBYFIN_EXPORTER_TEST_BACKEND=jellyfin .scripts/testenv.sh down
package acceptance_test

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) { os.Exit(runSuite(m)) }
