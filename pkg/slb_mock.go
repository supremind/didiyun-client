package pkg

import (
	"fmt"
	"context"

	"github.com/pborman/uuid"
)

type slbInfo struct {
	name string
	eip string
}

type mockSlbClient struct {
	slb map[string]*slbInfo
	count int
}

func (t *mockSlbClient) Create(ctx context.Context, regionID, zoneID, name string, bandwidth int64) (string, error) {
	id := uuid.NewUUID().String()
	t.count++
	t.slb[id] = &slbInfo{name: name, eip: fmt.Sprintf("192.168.0.%d", t.count)}
	return id, nil
}

func (t *mockSlbClient)	GetExternalIP(ctx context.Context, uuid string) (string, error) {
	s, ok := t.slb[uuid]
	if !ok {
		return "", fmt.Errorf("slb %s %w", uuid, NotFound)
	}
	return s.eip, nil
}

func (t *mockSlbClient)	Delete(ctx context.Context, uuid string) error {
	_, ok := t.slb[uuid]
	if !ok {
		return fmt.Errorf("slb %s %w", uuid, NotFound)
	}
	delete(t.slb, uuid)
	return nil
}

func (t *mockSlbClient)	SyncListeners(ctx context.Context, uuid string, listeners []*Listener, dc2Names []string) error {
	_, ok := t.slb[uuid]
	if !ok {
		return fmt.Errorf("slb %s %w", uuid, NotFound)
	}
	return nil
}

func (t *mockSlbClient)	SyncListenerMembers(ctx context.Context, uuid string, dc2Names []string) error {
	_, ok := t.slb[uuid]
	if !ok {
		return fmt.Errorf("slb %s %w", uuid, NotFound)
	}
	return nil
}