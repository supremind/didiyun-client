package pkg

import (
	"context"
	"fmt"

	"github.com/didiyun/didiyun-go-sdk/compute/v1"
	"github.com/pborman/uuid"
)

type ebsInfo struct {
	id      string
	dc2Name string
}

type mockEbsClient struct {
	ebs map[string]*ebsInfo
}

var _ EbsClient = (*mockEbsClient)(nil)

func (t *mockEbsClient) Create(ctx context.Context, regionID, zoneID, name, typ string, sizeGB int64) (string, error) {
	_, ok := t.ebs[name]
	if ok {
		return "", fmt.Errorf("%s already exist", name)
	}
	id := uuid.NewUUID().String()
	t.ebs[name] = &ebsInfo{id: id}
	return id, nil
}

func (t *mockEbsClient) Get(ctx context.Context, ebsUUID string) (*compute.EbsInfo, error) {
	for name, info := range t.ebs {
		if info.id == ebsUUID {
			ebs := &compute.EbsInfo{
				Name:    name,
				EbsUuid: ebsUUID,
			}
			if info.dc2Name != "" {
				ebs.Dc2 = &compute.Dc2Info{Name: info.dc2Name}
			}
			return ebs, nil
		}
	}

	return nil, fmt.Errorf("ebs %s not found", ebsUUID)
}

func (t *mockEbsClient) Delete(ctx context.Context, ebsUUID string) error {
	for n, e := range t.ebs {
		if ebsUUID == e.id {
			delete(t.ebs, n)
			return nil
		}
	}
	return fmt.Errorf("%s not found", ebsUUID)
}

func (t *mockEbsClient) Attach(ctx context.Context, ebsUUID, dc2Name string) (string, error) {
	for _, e := range t.ebs {
		if ebsUUID == e.id {
			if e.dc2Name != "" && e.dc2Name != dc2Name {
				return "", fmt.Errorf("%s attached to %s", ebsUUID, e.dc2Name)
			}
			e.dc2Name = dc2Name
			return "mock-device", nil
		}
	}
	return "", fmt.Errorf("%s not found", ebsUUID)
}

func (t *mockEbsClient) Detach(ctx context.Context, ebsUUID string) error {
	for _, e := range t.ebs {
		if ebsUUID == e.id {
			e.dc2Name = ""
			return nil
		}
	}
	return fmt.Errorf("%s not found", ebsUUID)
}

func (t *mockEbsClient) Expand(ctx context.Context, ebsUUID string, sizeGB int64) error {
	for _, e := range t.ebs {
		if ebsUUID == e.id {
			return nil
		}
	}
	return fmt.Errorf("%s not found", ebsUUID)
}
