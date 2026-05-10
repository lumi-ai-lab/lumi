package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestCronEditParsesScopedFlagsAfterValue(t *testing.T) {
	var gotPath string
	var gotQuery string
	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		data, _ := io.ReadAll(r.Body)
		gotBody = string(data)
		fmt.Fprint(w, `{"job":{"id":"cron-1","name":"Greeting","enabled":false,"state":{"runCount":0}}}`)
	}))
	defer server.Close()

	stdout, err := tempOutputFile(t)
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()

	err = runCronEdit([]string{
		"cron-1",
		"enabled",
		"false",
		"--api-base",
		server.URL,
		"--conversation-id",
		"conv-1",
	}, stdout)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/cron/jobs/cron-1" {
		t.Fatalf("path = %q, want /cron/jobs/cron-1", gotPath)
	}
	if gotQuery != "conversationId=conv-1" {
		t.Fatalf("query = %q, want conversationId=conv-1", gotQuery)
	}
	if !strings.Contains(gotBody, `"enabled":false`) {
		t.Fatalf("body = %q, want enabled false", gotBody)
	}
}

func tempOutputFile(t *testing.T) (*os.File, error) {
	t.Helper()
	return os.CreateTemp(t.TempDir(), "stdout")
}
