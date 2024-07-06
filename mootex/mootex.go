package mootex

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.etcd.io/etcd/client/pkg/v3/types"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/server/v3/embed"
)

type Client struct {
	client     *clientv3.Client
	embedClose func()
}

func NewClient(ctx context.Context, urls string) (c *Client, err error) {
	c = &Client{}

	var eps []string
	if !strings.HasPrefix(urls, "file://") {
		eps = strings.Split(urls, ",")
	} else {
		randURLs, err := types.NewURLs([]string{"http://127.0.0.1:0"})
		if err != nil {
			return nil, err
		}

		cfg := embed.NewConfig()
		cfg.ListenClientUrls = randURLs
		cfg.AdvertiseClientUrls = randURLs
		cfg.ListenPeerUrls = randURLs
		cfg.AdvertisePeerUrls = randURLs
		cfg.InitialCluster = cfg.InitialClusterFromName(cfg.Name)
		cfg.Dir = filepath.Join(strings.TrimPrefix(urls, "file://"), "tailscale.etcd")
		cfg.Logger = "zap" // set to avoid data race in the default logger

		if strings.HasPrefix(cfg.Dir, os.TempDir()) {
			// Well this is a pickle.
			// The tradeoff here is startup time vs. long-running efficiency.
			// Etcd does a leader election on startup even in single-node mode,
			// and the default value of ElectionMs is 1000 meaning it takes a
			// full second to start. That really hurts tests.
			//
			// But the election timeout must be 5x the heartbeat interval, so
			// to do a faster initial election, we have to commit to far more
			// frequent heartbeats (which are meaningless in a single-node
			// embedded cluster).
			//
			// So we crank the heartbeats to every 15ms if the data dir is
			// in $TMPDIR. The CPU overhead is minimal in tests and the
			// wall-time savings are huge.
			cfg.TickMs = 15
			cfg.ElectionMs = 75
		} else {
			cfg.TickMs = 50
			cfg.ElectionMs = 250
		}

		start := time.Now()
		e, err := embed.StartEtcd(cfg)
		if err != nil {
			return nil, fmt.Errorf("embedded server failed to start: %v", err)
		}

		c.embedClose = e.Close
		select {
		case <-e.Server.ReadyNotify():
		case <-ctx.Done():
			e.Server.Stop() // trigger a shutdown
			return nil, fmt.Errorf("embedded server took too long to start")
		}
		slog.Debug(fmt.Sprintf("etcd: embedded server started in %s (election timeout: %dms)", time.Since(start).Round(time.Microsecond), cfg.ElectionMs))
		eps = []string{"http://" + e.Clients[0].Addr().String()}
	}

	c.client, err = clientv3.New(clientv3.Config{Endpoints: eps})
	if err != nil {
		return nil, fmt.Errorf("etcd.New: %v", err)
	}

	return c, nil
}
