package mootex

import (
	"context"
	"fmt"
	"testing"
)

func TestNewClient(t *testing.T) {
	url := "file://" + t.TempDir()
	c1, err := NewClient(context.Background(), url)
	if err != nil {
		t.Errorf("NewClient() error = %v", err)
		return
	}
	url = "file://" + t.TempDir()
	c2, err := NewClient(context.Background(), url)
	if err != nil {
		t.Errorf("NewClient() error = %v", err)
		return
	}

	fmt.Println("created")

	if err := twoPhaseLocking(context.Background(), c1.client, c2.client); err != nil {
		t.Errorf("twoPhaseLocking() error = %v", err)
	}
}
