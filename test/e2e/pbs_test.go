//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	// image is the image built by `make docker-build`; `make e2e` builds it first.
	image = "pbs-tracer:local"
	// container is fixed so a container left by an aborted run is removed by the next one.
	container = "pbs-tracer-e2e"
	// dockerTimeout bounds every docker CLI call; the context is not the test's, because cleanup runs after it is canceled.
	dockerTimeout = time.Minute
)

// pbs is the Prebid Server container under test.
type pbs struct {
	url string
}

// startPBS runs the image on a free loopback port and waits for /status. The container is removed when
// the test ends; on failure the tail of the PBS log is attached to the test output.
func startPBS(t *testing.T) *pbs {
	t.Helper()
	removeContainer()
	combinedOutput(t, func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, "docker", "run", "--detach", "--name", container, "--publish", "127.0.0.1::8080", image)
	})
	t.Cleanup(func() {
		if t.Failed() {
			_, log := logs(t)
			t.Logf("PBS log tail:\n%s", tail(log, 30))
		}
		removeContainer()
	})

	addr := strings.Fields(combinedOutput(t, func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, "docker", "port", container, "8080/tcp")
	}))
	require.NotEmpty(t, addr, "no published port for 8080/tcp")
	p := &pbs{url: "http://" + addr[0]}
	require.Eventually(t, p.ready, 60*time.Second, 500*time.Millisecond, "PBS did not answer /status")
	return p
}

func removeContainer() {
	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()
	_ = exec.CommandContext(ctx, "docker", "rm", "--force", container).Run()
}

func (p *pbs) ready() bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url+"/status", http.NoBody)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// auction posts body to /openrtb2/auction and returns the response body.
func (p *pbs) auction(t *testing.T, body []byte) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url+"/openrtb2/auction", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, "auction response: %s", out)
	return out
}

// logs returns the container's stdout (trace packets) and stderr (PBS log) so far.
func logs(t *testing.T) (stdout, stderr []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()
	var out, errOut bytes.Buffer
	cmd := exec.CommandContext(ctx, "docker", "logs", container)
	cmd.Stdout, cmd.Stderr = &out, &errOut
	require.NoError(t, cmd.Run())
	return out.Bytes(), errOut.Bytes()
}

// combinedOutput runs the docker command built by newCmd under dockerTimeout and fails the test on error.
func combinedOutput(t *testing.T, newCmd func(context.Context) *exec.Cmd) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()
	cmd := newCmd(ctx)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s: %s", cmd, out)
	return string(out)
}

func tail(b []byte, lines int) string {
	all := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	return strings.Join(all[max(len(all)-lines, 0):], "\n")
}
