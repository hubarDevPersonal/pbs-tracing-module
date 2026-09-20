//go:build load

package load

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	container     = "pbs-tracer-loadbench"
	dockerTimeout = time.Minute
)

// pbsContainer is one Prebid Server started from the bench image with deploy/pbs.load.yaml.
type pbsContainer struct {
	URL        string // auction endpoint base
	MetricsURL string // prometheus
	AdminURL   string // pprof
}

// startPBS runs the image with the bench configuration. env adds PBS_* overrides (viper reads them).
// With stalledStdout the server's stdout is a FIFO nobody reads: after the 64 KiB pipe buffer every
// write blocks, which is the failure mode the module must survive.
func startPBS(t *testing.T, image string, env []string, stalledStdout bool) *pbsContainer {
	t.Helper()
	removeContainer()

	cfg, err := filepath.Abs("../../deploy/pbs.load.yaml")
	require.NoError(t, err)
	args := []string{
		"run", "--detach", "--name", container,
		"--publish", "127.0.0.1::8080", "--publish", "127.0.0.1::9100", "--publish", "127.0.0.1::6060",
		"--add-host=host.docker.internal:host-gateway", // Linux; Docker Desktop resolves it anyway
		"--volume", cfg + ":/usr/local/bin/pbs.yaml:ro",
	}
	for _, e := range env {
		args = append(args, "--env", e)
	}
	if stalledStdout {
		args = append(args, "--entrypoint", "sh", image, "-c",
			// mkfifo first, then a reader that opens the FIFO and never reads (opening for write would
			// otherwise block), then the server with stdout on the FIFO
			"mkfifo /tmp/stdout; (sleep infinity </tmp/stdout &); exec /usr/local/bin/prebid-server -stderrthreshold=INFO >/tmp/stdout")
	} else {
		args = append(args, image, "-stderrthreshold=INFO")
	}
	docker(t, func(ctx context.Context) *exec.Cmd {
		cmd := exec.CommandContext(ctx, "docker")
		cmd.Args = append(cmd.Args, args...) // built above from constants and bench flags
		return cmd
	})
	t.Cleanup(func() {
		if t.Failed() {
			_, stderr := containerLogs(t)
			t.Logf("PBS log tail:\n%s", tail(stderr, 30))
		}
		removeContainer()
	})

	p := &pbsContainer{
		URL:        "http://" + publishedAddr(t, "8080/tcp"),
		MetricsURL: "http://" + publishedAddr(t, "9100/tcp") + "/metrics",
		AdminURL:   "http://" + publishedAddr(t, "6060/tcp"),
	}
	require.Eventually(t, func() bool { return httpOK(p.URL + "/status") }, 60*time.Second, 500*time.Millisecond, "PBS did not answer /status")
	require.Eventually(t, func() bool { return httpOK(p.MetricsURL) }, 10*time.Second, 200*time.Millisecond, "metrics not served")
	return p
}

func publishedAddr(t *testing.T, port string) string {
	t.Helper()
	out := strings.Fields(docker(t, func(ctx context.Context) *exec.Cmd {
		cmd := exec.CommandContext(ctx, "docker", "port", container)
		cmd.Args = append(cmd.Args, port) // "8080/tcp", "9100/tcp" or "6060/tcp"
		return cmd
	}))
	require.NotEmpty(t, out, "no published port for %s", port)
	return out[0]
}

func httpOK(url string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
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

// metrics scrapes prometheus and sums every sample of each requested metric across its label sets.
func (p *pbsContainer) metrics(t *testing.T, names ...string) map[string]float64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.MetricsURL, http.NoBody)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	out := make(map[string]float64, len(names))
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		for _, n := range names {
			if strings.HasPrefix(line, n+"{") || strings.HasPrefix(line, n+" ") {
				fields := strings.Fields(line)
				v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
				require.NoError(t, err, "metric %s: %s", n, line)
				out[n] += v
			}
		}
	}
	require.NoError(t, sc.Err())
	return out
}

// memStats holds the runtime counters the bench reports, read from pprof's heap profile summary
// (PBS's prometheus registry carries no Go runtime collector).
type memStats struct {
	NumGC     float64
	HeapAlloc float64
	HeapInuse float64
	Sys       float64
}

func (p *pbsContainer) memStats(t *testing.T) memStats {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.AdminURL+"/debug/pprof/heap?debug=1", http.NoBody)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var m memStats
	fields := map[string]*float64{"NumGC": &m.NumGC, "HeapAlloc": &m.HeapAlloc, "HeapInuse": &m.HeapInuse, "Sys": &m.Sys}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		line := sc.Text() // "# HeapAlloc = 12345678"
		if !strings.HasPrefix(line, "# ") {
			continue
		}
		name, value, ok := strings.Cut(strings.TrimPrefix(line, "# "), " = ")
		if !ok {
			continue
		}
		if dst, want := fields[name]; want {
			v, err := strconv.ParseFloat(value, 64)
			require.NoError(t, err, "memstat %s: %s", name, line)
			*dst = v
		}
	}
	require.NoError(t, sc.Err())
	return m
}

// rss returns the container's resident memory in bytes as docker stats reports it.
func rss(t *testing.T) int64 {
	t.Helper()
	usage := strings.TrimSpace(docker(t, func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, "docker", "stats", "--no-stream", "--format", "{{.MemUsage}}", container)
	}))
	used := strings.TrimSpace(strings.SplitN(usage, "/", 2)[0])
	units := []struct {
		suffix string
		mul    float64
	}{{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"B", 1}}
	for _, u := range units {
		if strings.HasSuffix(used, u.suffix) {
			v, err := strconv.ParseFloat(strings.TrimSuffix(used, u.suffix), 64)
			require.NoError(t, err, "docker stats memory %q", usage)
			return int64(v * u.mul)
		}
	}
	t.Fatalf("unexpected docker stats memory %q", usage)
	return 0
}

// containerLogs returns the container's stdout (trace packets) and stderr (PBS log) so far.
func containerLogs(t *testing.T) (stdout, stderr []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()
	var out, errOut bytes.Buffer
	cmd := exec.CommandContext(ctx, "docker", "logs", container)
	cmd.Stdout, cmd.Stderr = &out, &errOut
	require.NoError(t, cmd.Run())
	return out.Bytes(), errOut.Bytes()
}

func removeContainer() {
	ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
	defer cancel()
	_ = exec.CommandContext(ctx, "docker", "rm", "--force", container).Run()
}

// docker runs one docker command built by newCmd under dockerTimeout and fails the test on error.
func docker(t *testing.T, newCmd func(context.Context) *exec.Cmd) string {
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

func mib(b int64) string { return fmt.Sprintf("%.0f MiB", float64(b)/(1<<20)) }

// cpuUsage returns the CPU time the container has consumed so far (cgroup v2 cpu.stat). The bench's
// generator and stub bidder run on the host, so this is Prebid Server's CPU alone.
func cpuUsage(t *testing.T) time.Duration {
	t.Helper()
	out := docker(t, func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, "docker", "exec", container, "cat", "/sys/fs/cgroup/cpu.stat")
	})
	for _, line := range strings.Split(out, "\n") {
		if name, value, ok := strings.Cut(line, " "); ok && name == "usage_usec" {
			usec, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
			require.NoError(t, err, "cpu.stat: %q", line)
			return time.Duration(usec) * time.Microsecond
		}
	}
	t.Fatalf("usage_usec not found in cpu.stat:\n%s", out)
	return 0
}

// dockerCPUs returns the number of CPUs available to containers.
func dockerCPUs(t *testing.T) int {
	t.Helper()
	out := strings.TrimSpace(docker(t, func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, "docker", "info", "--format", "{{.NCPU}}")
	}))
	n, err := strconv.Atoi(out)
	require.NoError(t, err, "docker info NCPU: %q", out)
	return n
}

// cpuProfile captures a CPU profile of the server for the given number of seconds and returns the
// path of the saved profile. It blocks for that long, so call it from a goroutine next to the load.
func (p *pbsContainer) cpuProfile(t *testing.T, dir string, seconds int) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(seconds+30)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/debug/pprof/profile?seconds=%d", p.AdminURL, seconds), http.NoBody)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode, "pprof profile")
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	path := filepath.Join(dir, fmt.Sprintf("cpu-%d.pprof", time.Now().UnixNano()))
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	return path
}

// pprofTop renders the top frames of a profile and the share of samples on paths through the module.
func pprofTop(t *testing.T, profile string, nodes int) (top, moduleShare string) {
	t.Helper()
	run := func(args ...string) string {
		ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, "go", "tool", "pprof")
		cmd.Args = append(cmd.Args, args...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "go tool pprof: %s", out)
		return string(out)
	}
	top = run("-top", "-nodecount="+strconv.Itoa(nodes), profile)
	focus := run("-top", "-nodecount=1", "-focus=test_tracer", profile) // symbols carry the directory name, not the package name
	for _, line := range strings.Split(focus, "\n") {
		if strings.HasPrefix(line, "Showing nodes accounting for") {
			moduleShare = strings.TrimSpace(line)
		}
	}
	return top, moduleShare
}
