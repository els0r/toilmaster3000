package main

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"testing"

	"github.com/els0r/toilmaster3000/internal/github"
	"github.com/stretchr/testify/require"
)

// resolveSelfLogin resolves the @me token via the gh seam; the Fake stands in
// for `gh api user` so this is provable without a real gh or network.
func TestResolveSelfLoginSuccess(t *testing.T) {
	fake := github.NewFake()
	fake.Login = "octocat"

	login, err := resolveSelfLogin(context.Background(), fake)
	require.NoError(t, err)
	require.Equal(t, "octocat", login)
}

// A failure resolving @me is a hard preflight error (never proceed without it).
func TestResolveSelfLoginError(t *testing.T) {
	fake := github.NewFake()
	fake.CurrentUserErr = errors.New("not authenticated")

	_, err := resolveSelfLogin(context.Background(), fake)
	require.Error(t, err)
	require.Contains(t, err.Error(), "gh api user")
}

// An empty login is rejected rather than silently accepted.
func TestResolveSelfLoginEmpty(t *testing.T) {
	fake := github.NewFake()
	fake.Login = ""

	_, err := resolveSelfLogin(context.Background(), fake)
	require.Error(t, err)
}

// listen binds a free port and returns a usable listener.
func TestListenBindsFreePort(t *testing.T) {
	ln, err := listen("localhost:0")
	require.NoError(t, err)
	defer ln.Close()
	require.NotNil(t, ln)
}

// A port already in use causes a clear startup failure instead of a silent one.
func TestListenPortInUse(t *testing.T) {
	// Bind a port first, then assert a second listen on the same address fails.
	first, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	defer first.Close()

	_, err = listen(first.Addr().String())
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot bind")
}

// checkGhAuth surfaces a failing `gh auth status` as a clear error (the auth
// check is injected, so no real gh is needed).
func TestCheckGhAuthFailsWhenUnauthenticated(t *testing.T) {
	if _, err := exec.LookPath("gh"); err != nil {
		t.Skip("gh not on PATH; LookPath gate would fire before the auth check")
	}
	err := checkGhAuth(context.Background(), func(context.Context) error {
		return errors.New("not logged in")
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "gh auth login")
}

// checkGhAuth passes when gh is present and the auth status check succeeds.
func TestCheckGhAuthPasses(t *testing.T) {
	if _, err := exec.LookPath("gh"); err != nil {
		t.Skip("gh not on PATH")
	}
	err := checkGhAuth(context.Background(), func(context.Context) error { return nil })
	require.NoError(t, err)
}
