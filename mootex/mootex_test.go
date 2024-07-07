package mootex

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"testing"

	clientv3 "go.etcd.io/etcd/client/v3"
	"golang.org/x/sync/errgroup"
)

func TestNewClient(t *testing.T) {
	ctx := context.Background()
	var clients []*clientv3.Client

	for i := 0; i < 3; i++ {
		url := "file://" + t.TempDir()
		c, err := NewClient(ctx, url)
		if err != nil {
			t.Errorf("NewClient() error = %v", err)
			return
		}
		clients = append(clients, c.C)
		t.Cleanup(c.embedClose)
	}
	t.Run("set", func(t *testing.T) {
		if err := updateGlobalKey(ctx, clients, "foo", "", "bar"); err != nil {
			t.Fatal(err)
		}

		resp, err := clients[0].KV.Get(ctx, "foo")
		if err != nil {
			t.Fatal(err)
		}
		if string(resp.Kvs[0].Value) != "bar" {
			t.Fatal("foo is not bar!")
		}
	})

	t.Run("update", func(t *testing.T) {
		if err := updateGlobalKey(ctx, clients, "foo", "", "baz"); err != nil {
			if err != ErrInvalidUpdate {
				t.Fatal(err)
			}
		}
		if err := updateGlobalKey(ctx, clients, "foo", "bar", "baz"); err != nil {
			t.Fatal(err)
		}
		resp, err := clients[0].KV.Get(ctx, "foo")
		if err != nil {
			t.Fatal(err)
		}
		if string(resp.Kvs[0].Value) != "baz" {
			t.Fatal("foo is not baz!")
		}
	})

	t.Run("transactional_series", func(t *testing.T) {
		eg, ctx := errgroup.WithContext(ctx)
		for i := 0; i < 5; i++ {
			eg.Go(func() error {
				for i := 0; i < 100; i++ {
					select {
					case <-ctx.Done():
						return ctx.Err()
					default:
					}
					resp, err := clients[0].Get(ctx, "key")
					if err != nil {
						return fmt.Errorf("error getting key %w", err)
					}
					var prev []byte
					var next []byte
					if resp.Count == 0 {
						next = []byte("1")
					} else {
						prev = resp.Kvs[0].Value
						next, err = validateAndAppendSeries(resp.Kvs[0].Value)
						if err != nil {
							return err
						}
					}
					if err := updateGlobalKey(ctx, clients, "key", string(prev), string(next)); err != nil {
						slog.Info("updateGlobalKey failed", "err", err)
						if errors.Is(err, ErrMismatchedValue) {
							return err
						}
					}
				}
				return nil
			})
		}

		if err := eg.Wait(); err != nil {
			t.Fatal(err)
		}

		resp, err := clients[0].Get(context.Background(), "key")
		if err != nil {
			t.Fatalf("error getting key %v", err)
		}
		fmt.Println(string(resp.Kvs[0].Value))
	})
}

func validateAndAppendSeries(series []byte) (next []byte, err error) {
	var prev int
	for _, v := range strings.Split(string(series), ",") {
		num, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("failed to parse value %q of %q: %w", v, series, err)
		}
		if num != prev+1 {
			return nil, fmt.Errorf("invalid series: %s", string(series))
		}
		prev = num
	}
	return append(series, []byte(","+strconv.Itoa(prev+1))...), nil
}
