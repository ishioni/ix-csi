package client

import (
	"context"
	"fmt"
	"time"
)

const (
	methodReplicationRunOnetime = "replication.run_onetime"
	methodCoreGetJobs           = "core.get_jobs"
)

// ReplicationRunOnetimeOptions contains the arguments needed for a local,
// one-shot ZFS send/receive. The source_datasets value is the dataset name
// without the temporary snapshot suffix; name_regex selects that snapshot.
//
// TrueNAS v25.10 exposes this operation through the websocket API as a job.
type ReplicationRunOnetimeOptions struct {
	Direction       string   `json:"direction"`
	Transport       string   `json:"transport"`
	SourceDatasets  []string `json:"source_datasets"`
	TargetDataset   string   `json:"target_dataset"`
	NameRegex       string   `json:"name_regex,omitempty"`
	Recursive       bool     `json:"recursive"`
	Properties      bool     `json:"properties"`
	Readonly        string   `json:"readonly"`
	RetentionPolicy string   `json:"retention_policy"`
	OnlyFromScratch bool     `json:"only_from_scratch"`
}

// Job represents the fields of a TrueNAS core job needed by the driver.
type Job struct {
	ID        int    `json:"id"`
	State     string `json:"state"`
	Error     string `json:"error"`
	Exception string `json:"exception"`
	Result    any    `json:"result"`
}

// RunReplicationOnetime starts a local one-shot replication job and returns its
// TrueNAS job ID.
func (c *Client) RunReplicationOnetime(ctx context.Context, options *ReplicationRunOnetimeOptions) (int, error) {
	var jobID int
	if err := c.Call(ctx, methodReplicationRunOnetime, []any{options}, &jobID); err != nil {
		return 0, fmt.Errorf("failed to start one-time replication: %w", err)
	}
	return jobID, nil
}

// GetJob retrieves a TrueNAS job by ID.
func (c *Client) GetJob(ctx context.Context, id int) (*Job, error) {
	filters := [][]any{{"id", "=", id}}
	options := &QueryOptions{Limit: 1}

	var jobs []Job
	if err := c.Call(ctx, methodCoreGetJobs, []any{filters, options}, &jobs); err != nil {
		return nil, fmt.Errorf("failed to get TrueNAS job %d: %w", id, err)
	}
	if len(jobs) == 0 {
		return nil, fmt.Errorf("TrueNAS job %d: %w", id, ErrNotFound)
	}
	return &jobs[0], nil
}

// WaitForJob waits until a TrueNAS job reaches a terminal state.
func (c *Client) WaitForJob(ctx context.Context, id int, interval time.Duration) (*Job, error) {
	if interval <= 0 {
		interval = time.Second
	}

	for {
		job, err := c.GetJob(ctx, id)
		if err != nil {
			return nil, err
		}
		switch job.State {
		case "SUCCESS", "FAILED", "ABORTED":
			return job, nil
		}

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
