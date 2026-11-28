package memstore

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestExtractionJobStoresReferenceNotMessageText(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	const text = "以后请叫我 小舟"
	source := ExtractSource{
		EventID:    BuildOriginEventID("pico", "extract-job-session", "msg-1", "user"),
		SessionKey: "extract-job-session", MessageID: "msg-1", Role: "user",
		DomainID: DomainLegacyUnassigned,
	}
	jobID, err := s.EnqueueExtractionJob(ctx, source, text)
	if err != nil {
		t.Fatal(err)
	}
	if jobID == "" {
		t.Fatal("empty extraction job id")
	}
	jobs, err := s.ListJobs(ctx, JobPending, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs = %+v, err=%v", jobs, err)
	}
	if strings.Contains(jobs[0].Payload, text) {
		t.Fatal("extraction job payload contains raw message text")
	}
	var payload ExtractionJobPayload
	if err := json.Unmarshal([]byte(jobs[0].Payload), &payload); err != nil {
		t.Fatal(err)
	}
	if !s.MatchExtractionSource(payload, text) {
		t.Fatal("job digest does not match the original source")
	}
	if s.MatchExtractionSource(payload, text+"改") {
		t.Fatal("job digest matched modified source")
	}
}
