// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/httpapi"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/project"
)

// freeAddr returns a loopback address nothing listens on.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	return addr
}

// TestDrainFinishesRequestsAndEndsStreams checks what a deploy does to a server
// with work open. A request in flight when draining starts keeps its context
// and completes with its normal status, and an open event stream ends with its
// end frame promptly instead of holding the drain.
func TestDrainFinishesRequestsAndEndsStreams(t *testing.T) {
	ctx := context.Background()
	jobs := job.NewMemStore()
	projects := project.NewMemStore()
	p, err := projects.Create(ctx, "sub", "t")
	if err != nil {
		t.Fatal(err)
	}
	if err := projects.SetPublic(ctx, p.ID, project.SignOff{By: "sub", AttestationVersion: "v1"}); err != nil {
		t.Fatal(err)
	}
	j, err := jobs.Enqueue(ctx, job.Submission{Principal: "sub", Ref: "ref", PaperKey: "doi:10.0/ref", Kind: job.KindAudit, Project: p.ID})
	if err != nil {
		t.Fatal(err)
	}

	draining := make(chan struct{})
	startDraining := sync.OnceFunc(func() { close(draining) })
	router := httpapi.NewRouter(httpapi.Deps{
		Jobs: jobs, Projects: projects,
		Gate:     httpapi.GateFunc(func(*http.Request) (bool, string) { return true, "" }),
		Draining: draining,
	})
	entered, release := make(chan struct{}), make(chan struct{})
	mux := http.NewServeMux()
	mux.Handle("/", router.Handler())
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		if err := r.Context().Err(); err != nil {
			t.Errorf("a request in flight had its context ended by the drain: %v", err)
			http.Error(w, "context ended", http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, "done")
	})

	addr := freeAddr(t)
	srv := newServer(addr, mux)
	serveCtx, stopServing := context.WithCancel(ctx)
	defer stopServing()
	served := make(chan error, 1)
	go func() { served <- serve(serveCtx, srv, startDraining) }()
	base := "http://" + addr
	waitUp(t, base+"/healthz")

	type reply struct {
		status int
		body   string
		err    error
	}
	slow := make(chan reply, 1)
	go func() {
		resp, err := http.Get(base + "/slow")
		if err != nil {
			slow <- reply{err: err}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		slow <- reply{status: resp.StatusCode, body: string(body), err: err}
	}()
	<-entered

	stream, err := http.Get(base + "/jobs/" + j.ID + "/events")
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer func() { _ = stream.Body.Close() }()
	frames := bufio.NewScanner(stream.Body)
	ended := make(chan string, 1)
	opened := make(chan struct{})
	go func() {
		var event, last string
		var once sync.Once
		for frames.Scan() {
			line := frames.Text()
			once.Do(func() { close(opened) })
			if after, ok := strings.CutPrefix(line, "event: "); ok {
				event = after
			}
			if strings.HasPrefix(line, "data: ") && event == "end" {
				last = strings.TrimPrefix(line, "data: ")
			}
		}
		ended <- last
	}()
	select {
	case <-opened:
	case <-time.After(2 * time.Second):
		t.Fatal("the stream sent nothing")
	}

	stopServing() // SIGTERM
	select {
	case last := <-ended:
		if last != `{"reason":"unavailable"}` {
			t.Fatalf("stream end frame = %q, want reason unavailable", last)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the open event stream was still open after draining started")
	}

	select {
	case got := <-slow:
		t.Fatalf("the in-flight request ended before it was released: %+v", got)
	default:
	}
	close(release)
	select {
	case got := <-slow:
		if got.err != nil || got.status != http.StatusOK || got.body != "done" {
			t.Fatalf("in-flight request = %+v, want 200 done", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the in-flight request never completed")
	}
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("serve: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serve did not return after the drain")
	}
}

// waitUp blocks until url answers.
func waitUp(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err := http.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("server never came up: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestAwaitStopSaysWhenALoopDidNotStop checks that a loop still writing when
// the wait ends is reported, which is what makes run keep the pool open, and
// that one that stopped is not.
func TestAwaitStopSaysWhenALoopDidNotStop(t *testing.T) {
	stopped := make(chan struct{})
	close(stopped)
	expired, cancel := context.WithCancel(context.Background())
	cancel()
	if err := awaitStop(expired, "the worker", stopped); err != nil {
		t.Fatalf("a loop that stopped was reported: %v", err)
	}
	if err := awaitStop(expired, "the worker", make(chan struct{})); err == nil {
		t.Fatal("a loop that never stopped was not reported")
	}
}
