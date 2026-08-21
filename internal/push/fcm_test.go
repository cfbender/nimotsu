package push

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestSendAllReportsPartialFailure(t *testing.T) {
	sender := &Sender{
		projectID: "test-project",
		httpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			status := http.StatusOK
			responseBody := "{}"
			if strings.Contains(string(body), "bad-token") {
				status = http.StatusNotFound
				responseBody = `{"error":"unregistered"}`
			}
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(responseBody)), Header: http.Header{}}, nil
		})},
	}

	sent, err := sender.SendAll(t.Context(), []string{"good-token", "bad-token", "good-token"}, "Title", "Body", "pkg_1")
	if sent != 2 {
		t.Fatalf("sent = %d, want 2", sent)
	}
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("err = %v, want joined HTTP 404 failure", err)
	}
}

func TestSendAllSucceedsForAllTokens(t *testing.T) {
	requests := 0
	sender := &Sender{
		projectID: "test-project",
		httpClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{}")), Header: http.Header{}}, nil
		})},
	}

	sent, err := sender.SendAll(t.Context(), []string{"one", "two"}, "Title", "Body", "pkg_1")
	if err != nil {
		t.Fatal(err)
	}
	if sent != 2 || requests != 2 {
		t.Fatalf("sent = %d, requests = %d, want 2 and 2", sent, requests)
	}
}
