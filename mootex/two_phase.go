package mootex

import (
	"context"
	"errors"
	"fmt"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

func twoPhaseLocking(ctx context.Context, client1, client2 *clientv3.Client) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Define the keys and values to update
	key1 := "key1"
	value1 := "new_value1"
	key2 := "key2"
	value2 := "new_value2"

	// Phase 1: Acquire Locks
	txn1 := client1.Txn(ctx).If(clientv3.Compare(clientv3.CreateRevision(key1+"_lock"), "=", 0)).
		Then(clientv3.OpPut(key1+"_lock", "locked"))
	txn2 := client2.Txn(ctx).If(clientv3.Compare(clientv3.CreateRevision(key2+"_lock"), "=", 0)).
		Then(clientv3.OpPut(key2+"_lock", "locked"))

	// Execute transactions to acquire locks
	resp1, err := txn1.Commit()
	if err != nil {
		return fmt.Errorf("failed to acquire lock on cluster1: %w", err)
	}
	if !resp1.Succeeded {
		return errors.New("lock already held on cluster1")
	}

	resp2, err := txn2.Commit()
	if err != nil {
		// Release lock on cluster1 if cluster2 fails
		client1.Delete(ctx, key1+"_lock")
		return fmt.Errorf("failed to acquire lock on cluster2: %w", err)
	}
	if !resp2.Succeeded {
		// Release lock on cluster1 if cluster2 fails
		client1.Delete(ctx, key1+"_lock")
		return errors.New("lock already held on cluster2")
	}

	// Phase 2: Update Values
	txn1 = client1.Txn(ctx).Then(clientv3.OpPut(key1, value1), clientv3.OpDelete(key1+"_lock"))
	txn2 = client2.Txn(ctx).Then(clientv3.OpPut(key2, value2), clientv3.OpDelete(key2+"_lock"))

	// Execute transactions to update values
	resp1, err = txn1.Commit()
	if err != nil {
		return fmt.Errorf("failed to update value on cluster1: %w", err)
	}
	if !resp1.Succeeded {
		return errors.New("failed to commit update on cluster1")
	}

	resp2, err = txn2.Commit()
	if err != nil {
		return fmt.Errorf("failed to update value on cluster2: %w", err)
	}
	if !resp2.Succeeded {
		return errors.New("failed to commit update on cluster2")
	}

	return nil
}
