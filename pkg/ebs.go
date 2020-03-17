package pkg

import (
	"context"
	"fmt"

	"github.com/didiyun/didiyun-go-sdk/base/v1"
	"github.com/didiyun/didiyun-go-sdk/compute/v1"
	"k8s.io/klog"
)

type EbsClient interface {
	Create(ctx context.Context, regionID, zoneID, name, typ string, sizeGB int64) (string, error)
	Delete(ctx context.Context, ebsUUID string) error
	Attach(ctx context.Context, ebsUUID, dc2Name string) (string, error)
	Detach(ctx context.Context, ebsUUID string) error
}

type ebsClient struct {
	cli compute.EbsClient
	helper
}

func (t *ebsClient) Create(ctx context.Context, regionID, zoneID, name, typ string, sizeGB int64) (string, error) {
	klog.V(4).Infof("creating ebs %s, type %s, size %d GB", name, typ, sizeGB)
	req := &compute.CreateEbsRequest{
		Header:       &base.Header{RegionId: regionID, ZoneId: zoneID},
		Count:        1,
		AutoContinue: false,
		PayPeriod:    0,
		Name:         name,
		Size:         sizeGB,
		DiskType:     typ,
	}
	resp, e := t.cli.CreateEbs(ctx, req)
	if e != nil {
		return "", fmt.Errorf("create ebs error %w", e)
	}
	if resp.Error.Errno != 0 {
		return "", fmt.Errorf("create ebs error %s (%d)", resp.Error.Errmsg, resp.Error.Errno)
	}

	job, e := t.waitForJob(ctx, resp.Data[0], regionID, zoneID)
	if e != nil {
		return "", e
	}
	if !job.Success {
		if job.ResourceUuid != "" { // not success, but still got uuid that already created
			return job.ResourceUuid, nil
		}
		return "", fmt.Errorf("failed to create ebs: %s", job.Result)
	}
	return job.ResourceUuid, nil
}

func (t *ebsClient) attachedDevice(ctx context.Context, ebsUUID, dc2Name string) (string, error) {
	klog.V(4).Infof("getting ebs %s", ebsUUID)
	req := &compute.GetEbsByUuidRequest{
		EbsUuid: ebsUUID,
	}
	resp, e := t.cli.GetEbsByUuid(ctx, req)
	if e != nil {
		return "", fmt.Errorf("get ebs error %w", e)
	}
	if resp.Error.Errno != 0 {
		return "", fmt.Errorf("get ebs error %s (%d)", resp.Error.Errmsg, resp.Error.Errno)
	}

	if len(resp.Data) == 0 {
		klog.V(4).Infof("ebs %s not found", ebsUUID)
		return "", nil
	}

	dc2 := resp.Data[0].GetDc2()
	if dc2 == nil {
		klog.V(4).Infof("ebs %s is already detached", ebsUUID)
		return "", nil
	}
	dn := resp.Data[0].GetDeviceName()
	klog.V(4).Infof("ebs %s is already attached to %s, device %s", ebsUUID, dc2.GetName(), dn)

	// not attached to target dc2
	if dc2Name != "" && dc2.GetName() != dc2Name {
		return "", nil
	}
	// attached to any dc2
	return dn, nil
}

func (t *ebsClient) Delete(ctx context.Context, ebsUUID string) error {
	klog.V(4).Infof("deleting ebs %s", ebsUUID)
	req := &compute.DeleteEbsRequest{
		Ebs: []*compute.DeleteEbsRequest_Input{{EbsUuid: ebsUUID}},
	}
	resp, e := t.cli.DeleteEbs(ctx, req)
	if e != nil {
		return fmt.Errorf("delete ebs error %w", e)
	}
	if resp.Error.Errno != 0 {
		return fmt.Errorf("delete ebs error %s (%d)", resp.Error.Errmsg, resp.Error.Errno)
	}

	job, e := t.waitForJob(ctx, resp.Data[0], "", "")
	if e != nil {
		return e
	}
	if !job.Success { // not found etc.
		return fmt.Errorf("failed to delete ebs: %s", job.Result)
	}
	return nil
}

func (t *ebsClient) Attach(ctx context.Context, ebsUUID, dc2Name string) (string, error) {
	klog.V(4).Infof("attaching ebs %s to dc2 %s", ebsUUID, dc2Name)
	dc2UUID, e := t.getDc2UUIDByName(ctx, dc2Name)
	if e != nil {
		return "", e
	}

	req := &compute.AttachEbsRequest{
		Ebs: []*compute.AttachEbsRequest_Input{{EbsUuid: ebsUUID, Dc2Uuid: dc2UUID}},
	}
	resp, e := t.cli.AttachEbs(ctx, req)
	if e != nil {
		return "", fmt.Errorf("attach ebs error %w", e)
	}
	if resp.Error.Errno != 0 {
		return "", fmt.Errorf("attach ebs error %s (%d)", resp.Error.Errmsg, resp.Error.Errno)
	}

	job, e := t.waitForJob(ctx, resp.Data[0], "", "")
	if e != nil {
		return "", e
	}

	// whether job.Success or not, need check attached device
	device, e := t.attachedDevice(ctx, ebsUUID, dc2Name)
	if e != nil {
		return "", e
	}
	if device != "" {
		return device, nil
	}
	return "", fmt.Errorf("failed to attach ebs: %s", job.Result)
}

func (t *ebsClient) Detach(ctx context.Context, ebsUUID string) error {
	klog.V(4).Infof("detaching ebs %s", ebsUUID)
	req := &compute.DetachEbsRequest{
		Ebs: []*compute.DetachEbsRequest_Input{{EbsUuid: ebsUUID}},
	}
	resp, e := t.cli.DetachEbs(ctx, req)
	if e != nil {
		return fmt.Errorf("detach ebs error %w", e)
	}
	if resp.Error.Errno != 0 {
		return fmt.Errorf("detach ebs error %s (%d)", resp.Error.Errmsg, resp.Error.Errno)
	}

	job, e := t.waitForJob(ctx, resp.Data[0], "", "")
	if e != nil {
		return e
	}

	device, e := t.attachedDevice(ctx, ebsUUID, "")
	if e != nil {
		return e
	}
	if device == "" {
		return nil
	}
	return fmt.Errorf("failed to detach ebs: %s", job.Result)
}
