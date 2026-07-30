package client

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRunReplicationOnetime(t *testing.T) {
	mock := NewMockTrueNASServer()
	defer mock.Close()
	mock.SetResponse(methodReplicationRunOnetime, MockResponse{Result: 42})

	client := connectTestClient(t, mock)
	jobID, err := client.RunReplicationOnetime(testContext(t), &ReplicationRunOnetimeOptions{
		Direction:       "PUSH",
		Transport:       "LOCAL",
		SourceDatasets:  []string{"tank/source"},
		TargetDataset:   "tank/detached/source/snapshot",
		NameRegex:       "^csi-detached-snapshot-snap$",
		Recursive:       false,
		Properties:      false,
		Readonly:        "IGNORE",
		RetentionPolicy: "NONE",
		OnlyFromScratch: true,
	})

	assertNoError(t, err)
	assertEqual(t, jobID, 42)

	requests := mock.GetRequestsByMethod(methodReplicationRunOnetime)
	assertLen(t, requests, 1)
	var params []any
	if err := json.Unmarshal(requests[0].Params, &params); err != nil {
		t.Fatalf("failed to decode params: %v", err)
	}
	options := params[0].(map[string]any)
	assertEqual(t, options["transport"], "LOCAL")
	assertEqual(t, options["target_dataset"], "tank/detached/source/snapshot")
	assertEqual(t, options["only_from_scratch"], true)
}

func TestWaitForJob(t *testing.T) {
	mock := NewMockTrueNASServer()
	defer mock.Close()
	mock.SetResponse(methodCoreGetJobs, MockResponse{
		Result: []Job{{ID: 42, State: "SUCCESS"}},
	})

	client := connectTestClient(t, mock)
	job, err := client.WaitForJob(testContext(t), 42, time.Millisecond)

	assertNoError(t, err)
	assertNotNil(t, job)
	assertEqual(t, job.ID, 42)
	assertEqual(t, job.State, "SUCCESS")
}
