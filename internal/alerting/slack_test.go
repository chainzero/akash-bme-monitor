package alerting

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chainzero/akash-bme-monitor/internal/types"
)

// fakeWebhook records the text of every message posted to it.
type fakeWebhook struct {
	mu     sync.Mutex
	texts  []string
	status int
}

func (f *fakeWebhook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var msg struct {
		Text string `json:"text"`
	}
	json.NewDecoder(r.Body).Decode(&msg)

	f.mu.Lock()
	defer f.mu.Unlock()
	f.texts = append(f.texts, msg.Text)
	if f.status != 0 {
		w.WriteHeader(f.status)
		return
	}
	w.Write([]byte("ok"))
}

func (f *fakeWebhook) posts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.texts...)
}

func newTestSlack(t *testing.T) (*Slack, *fakeWebhook) {
	t.Helper()
	fw := &fakeWebhook{}
	srv := httptest.NewServer(fw)
	t.Cleanup(srv.Close)
	return NewSlack(srv.URL), fw
}

func TestSend_PostsFormattedMessageAndRecordsCooldown(t *testing.T) {
	s, fw := newTestSlack(t)
	s.Send(types.Alert{Key: "k", Severity: types.SeverityWarning, Title: "TITLE", Body: "body text"})

	posts := fw.posts()
	if len(posts) != 1 {
		t.Fatalf("expected 1 post, got %d", len(posts))
	}
	want := types.SeverityWarning.Emoji() + " *TITLE*\n\nbody text"
	if posts[0] != want {
		t.Errorf("post text = %q, want %q", posts[0], want)
	}
	entry, ok := s.cooldowns["k"]
	if !ok || entry.severity != types.SeverityWarning {
		t.Errorf("cooldown entry = %+v (exists=%v), want Warning", entry, ok)
	}
}

func TestSend_SuppressedWithinCooldown(t *testing.T) {
	s, fw := newTestSlack(t)
	a := types.Alert{Key: "k", Severity: types.SeverityWarning, Title: "T", Body: "B"}
	s.Send(a)
	s.Send(a)

	if n := len(fw.posts()); n != 1 {
		t.Errorf("expected duplicate to be suppressed (1 post), got %d", n)
	}
}

func TestSend_EscalationPostsImmediately(t *testing.T) {
	s, fw := newTestSlack(t)
	s.Send(types.Alert{Key: "k", Severity: types.SeverityInfo, Title: "T", Body: "B"})
	s.Send(types.Alert{Key: "k", Severity: types.SeverityCritical, Title: "T", Body: "B"})

	if n := len(fw.posts()); n != 2 {
		t.Errorf("expected escalation to post (2 posts), got %d", n)
	}
	if s.cooldowns["k"].severity != types.SeverityCritical {
		t.Errorf("cooldown severity = %s, want Critical", s.cooldowns["k"].severity)
	}
}

func TestSend_FailedPostDoesNotRecordCooldown(t *testing.T) {
	s, fw := newTestSlack(t)
	fw.status = http.StatusInternalServerError
	a := types.Alert{Key: "k", Severity: types.SeverityWarning, Title: "T", Body: "B"}
	s.Send(a)

	if _, ok := s.cooldowns["k"]; ok {
		t.Error("cooldown recorded after failed post; the alert would be silently dropped on retry")
	}

	fw.status = 0
	s.Send(a)
	if n := len(fw.posts()); n != 2 {
		t.Errorf("expected retry to post after failure (2 attempts), got %d", n)
	}
}

func TestSend_ZeroTimeDefaultsToNow(t *testing.T) {
	s, _ := newTestSlack(t)
	before := time.Now()
	s.Send(types.Alert{Key: "k", Severity: types.SeverityInfo, Title: "T", Body: "B"})

	if sentAt := s.cooldowns["k"].sentAt; sentAt.Before(before) {
		t.Errorf("sentAt = %v, want >= %v", sentAt, before)
	}
}

func TestResolve_PostsAndClearsWhenAlertOutstanding(t *testing.T) {
	s, fw := newTestSlack(t)
	s.Send(types.Alert{Key: "k", Severity: types.SeverityWarning, Title: "T", Body: "B"})
	s.Resolve("k", "RESOLVED", "all good")

	posts := fw.posts()
	if len(posts) != 2 {
		t.Fatalf("expected 2 posts (alert + resolve), got %d", len(posts))
	}
	want := types.SeverityResolved.Emoji() + " *RESOLVED*\n\nall good"
	if posts[1] != want {
		t.Errorf("resolve text = %q, want %q", posts[1], want)
	}
	if _, ok := s.cooldowns["k"]; ok {
		t.Error("cooldown entry still present after Resolve")
	}
}

func TestResolve_NoOpWithoutPriorAlert(t *testing.T) {
	s, fw := newTestSlack(t)
	s.Resolve("never-sent", "RESOLVED", "body")

	if n := len(fw.posts()); n != 0 {
		t.Errorf("expected no post for key with no outstanding alert, got %d", n)
	}
}

func TestResolve_NextSendPostsImmediately(t *testing.T) {
	s, fw := newTestSlack(t)
	a := types.Alert{Key: "k", Severity: types.SeverityWarning, Title: "T", Body: "B"}
	s.Send(a)
	s.Resolve("k", "R", "B")
	s.Send(a)

	if n := len(fw.posts()); n != 3 {
		t.Errorf("expected alert, resolve, re-alert (3 posts), got %d", n)
	}
}

func TestPost_AlwaysSendsWithoutCooldown(t *testing.T) {
	s, fw := newTestSlack(t)
	s.Post("REPORT", "line 1")
	s.Post("REPORT", "line 1")

	posts := fw.posts()
	if len(posts) != 2 {
		t.Fatalf("expected 2 posts, got %d", len(posts))
	}
	if !strings.HasPrefix(posts[0], "ℹ️ *REPORT*") {
		t.Errorf("post text = %q, want ℹ️ *REPORT* prefix", posts[0])
	}
	if len(s.cooldowns) != 0 {
		t.Errorf("Post recorded cooldown state: %v", s.cooldowns)
	}
}
