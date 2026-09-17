package main

import (
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestParseConfig_Defaults(t *testing.T) {
	cfg, err := parseConfig(nil, env(nil), io.Discard)
	require.NoError(t, err)
	assert.Equal(t, config{URL: defaultURL, BodyPath: defaultBody, RPS: defaultRPS, Duration: defaultDuration, Concurrency: defaultConcurrency, Timeout: defaultTimeout}, cfg)
	assert.Equal(t, defaultDuration+defaultTimeout+shutdownGrace, cfg.hardDeadline())
}

func TestParseConfig_EnvThenFlagsPrecedence(t *testing.T) {
	e := env(map[string]string{"LOADGEN_RPS": "2.5", "LOADGEN_DURATION": "10s", "LOADGEN_URL": "http://env:1/a", "LOADGEN_CONCURRENCY": "3", "LOADGEN_TIMEOUT": "2s", "LOADGEN_OUT": "env.json"})
	cfg, err := parseConfig([]string{"-rps", "7", "-out", "flag.json"}, e, io.Discard)
	require.NoError(t, err)
	assert.Equal(t, 7.0, cfg.RPS, "flag overrides env")
	assert.Equal(t, "flag.json", cfg.Out)
	assert.Equal(t, 10*time.Second, cfg.Duration, "env overrides default")
	assert.Equal(t, "http://env:1/a", cfg.URL)
	assert.Equal(t, 3, cfg.Concurrency)
	assert.Equal(t, 2*time.Second, cfg.Timeout)
}

func TestParseConfig_Errors(t *testing.T) {
	testCases := []struct {
		name string
		args []string
		env  map[string]string
		want string
	}{
		{name: "bad env number", env: map[string]string{"LOADGEN_RPS": "fast"}, want: "LOADGEN_RPS"},
		{name: "bad env duration", env: map[string]string{"LOADGEN_DURATION": "10"}, want: "LOADGEN_DURATION"},
		{name: "zero rps", args: []string{"-rps", "0"}, want: "rps must be positive"},
		{name: "negative duration", args: []string{"-duration", "-1s"}, want: "duration must be positive"},
		{name: "too much concurrency", args: []string{"-concurrency", "100000"}, want: "concurrency must be in"},
		{name: "empty url", args: []string{"-url", ""}, want: "url must not be empty"},
		{name: "positional args", args: []string{"extra"}, want: "unexpected positional"},
		{name: "unknown flag", args: []string{"-nope"}, want: "flag provided but not defined"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseConfig(tc.args, env(tc.env), io.Discard)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}
