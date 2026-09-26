package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNetworkRefreshDuringCollection(t *testing.T) {
	for _, outcome := range []string{"success", "read-error", "invalid-data", "cancel"} {
		t.Run(outcome, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			before, _ := parseNetworkSnapshot(networkCounterFixture(100, 1<<20, 0), time.Now().Add(-time.Second))
			previous, _ := parseNetworkSnapshot(networkCounterFixture(101, 2<<20, 0), time.Now())
			applyNetworkSnapshotRates(&previous, &before)
			s := &Session{ctx: ctx, networkBackground: true, networkPrevious: &previous}
			started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			var reads atomic.Int32
			go func() {
				_, err := s.collectNetworkSnapshot(ctx, func(ctx context.Context, _ string) ([]byte, error) {
					reads.Add(1)
					close(started)
					select {
					case <-release:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
					switch outcome {
					case "read-error":
						return nil, errors.New("controlled network read failure")
					case "invalid-data":
						return []byte("invalid counters"), nil
					default:
						return networkCounterFixture(102, 7<<20, 0), nil
					}
				})
				done <- err
			}()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("sampler did not start")
			}
			// A slow SSH result must not block local cache readers or spawn more
			// remote commands. They retain the last real observation and its age.
			var readers sync.WaitGroup
			for i := 0; i < 32; i++ {
				readers.Add(1)
				go func() {
					defer readers.Done()
					view, err := s.networkForDisplay(context.Background())
					if err != nil || !view.SampleInProgress || !view.Cached || view.NetworkRX != 1<<20 || view.SampledAt != previous.SampledAt {
						t.Errorf("in-flight cache lost its status or previous rate: %+v %v", view, err)
					}
					data, _ := json.Marshal(view)
					var wire map[string]any
					_ = json.Unmarshal(data, &wire)
					if wire["sampleInProgress"] != true {
						t.Error("refresh status missing from frontend response")
					}
				}()
			}
			readersDone := make(chan struct{})
			go func() { readers.Wait(); close(readersDone) }()
			select {
			case <-readersDone:
			case <-time.After(time.Second):
				t.Fatal("cache reads waited for remote collection")
			}
			if reads.Load() != 1 {
				t.Fatal("cache refresh started extra SSH collections")
			}
			if outcome == "cancel" {
				cancel()
			} else {
				close(release)
			}
			select {
			case err := <-done:
				if (err != nil) != (outcome != "success") {
					t.Fatal("unexpected collection outcome", err)
				}
			case <-time.After(time.Second):
				t.Fatal("sampler did not finish")
			}
			view, err := s.networkForDisplay(context.Background())
			if err != nil || view.SampleInProgress || reads.Load() != 1 {
				t.Fatalf("completion retained fast polling or restarted collection: %+v %v", view, err)
			}
			if outcome == "success" && (!view.SampleReady || view.NetworkRX != 5<<20) {
				t.Fatal("completed rate was not published", view)
			}
			if (outcome == "read-error" || outcome == "invalid-data") && (view.SampleError == "" || view.SampleReady || view.NetworkRX != 0) {
				t.Fatal("failed observation displayed as a measured rate", view)
			}
		})
	}
}
