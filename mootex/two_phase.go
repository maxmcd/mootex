package mootex

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	clientv3 "go.etcd.io/etcd/client/v3"
)

var ErrMismatchedValue = errors.New("values are mismatched between clusters")
var ErrInvalidUpdate = errors.New("initial key value does not match provided value")

func updateGlobalKey(ctx context.Context, clients []*clientv3.Client, key, oldValue, newValue string) error {
	const lockTTLSeconds = 30

	// Acquire locks to change the specific key. Fetch the current value of the
	// key so that we can restore if needed.
	var txns []clientv3.Txn
	var leases []clientv3.LeaseID
	for _, client := range clients {
		// Create a lease with TTL.
		leaseResp, err := client.Grant(ctx, lockTTLSeconds)
		if err != nil {
			releaseLeases(clients, ctx, leases)
			return fmt.Errorf("failed to create lease: %w", err)
		}
		leases = append(leases, leaseResp.ID)
		txns = append(txns, client.Txn(ctx).If(
			clientv3.Compare(clientv3.CreateRevision(key+"_lock"), "=", 0),
		).
			Then(
				clientv3.OpGet(key),
				clientv3.OpPut(key+"_lock", "locked", clientv3.WithLease(leaseResp.ID)),
			),
		)
	}

	// TODO: does this handle nil vs empty key correctly?
	prevValues := make([][]byte, len(clients))
	for i, txn := range txns {
		releaseAcquiredLocks := func() {
			for _, client := range clients[:i+1] {
				if _, err := client.Delete(context.Background(), key+"_lock"); err != nil {
					slog.Error("error deleting initial lock", "err", err)
				}
			}
		}
		resp, err := txn.Commit()
		if err != nil {
			err = fmt.Errorf("failed to acquire lock on cluster: %w", err)
		}
		if err == nil && !resp.Succeeded {
			err = fmt.Errorf("lock already held on cluster")
		}
		if err != nil {
			// Bailing, release all acquired locks.
			releaseAcquiredLocks()
			return err
		}
		rr := resp.Responses[0].GetResponseRange()
		if rr.Count != 0 {
			prevValues[i] = rr.Kvs[0].Value
			if string(prevValues[i]) != oldValue {
				// Bailing, release all acquired locks.
				releaseAcquiredLocks()
				return ErrInvalidUpdate
			}
			if i > 0 && string(prevValues[i]) != string(prevValues[i-1]) {
				// Bailing, release all acquired locks.
				releaseAcquiredLocks()
				return ErrMismatchedValue
			}
		}
	}

	txns = nil
	for _, client := range clients {
		txns = append(txns, client.Txn(ctx).Then(clientv3.OpPut(key, newValue), clientv3.OpDelete(key+"_lock")))
	}

	// If the initial value was nil then delete it, otherwise restore the
	// previous value.
	var rollbackOp clientv3.Op
	if prevValues[0] == nil {
		rollbackOp = clientv3.OpDelete(key)
	} else {
		rollbackOp = clientv3.OpPut(key, string(prevValues[0]))
	}

	for i, txn := range txns {
		resp, err := txn.Commit()
		if err != nil {
			err = fmt.Errorf("failed to set value on cluster: %w", err)
		}
		if err == nil && !resp.Succeeded {
			err = fmt.Errorf("failed to commit update on cluster")
		}
		if err != nil {
			// Roll back all committed changes.
			for _, client := range clients[:i] {
				client.Txn(ctx).If(
					clientv3.Compare(clientv3.Value(key), "=", newValue),
				).Then(
					rollbackOp,
				).Commit()
			}
			// Release all locks still acquired
			for _, client := range clients[i:] {
				client.Delete(ctx, key+"_lock")
			}
			return err
		}
	}
	return nil
}

func releaseLeases(clients []*clientv3.Client, ctx context.Context, leases []clientv3.LeaseID) {
	for i, client := range clients {
		if i < len(leases) {
			_, err := client.Revoke(ctx, leases[i])
			if err != nil {
				slog.Warn(fmt.Sprintf("Failed to revoke lease: %v", err))
			}
		}
	}
}
