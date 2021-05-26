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
	Get(ctx context.Context, ebsUUID string) (*compute.EbsInfo, error)
	Delete(ctx context.Context, ebsUUID string) error
	Attach(ctx context.Context, ebsUUID, dc2Ip string) (string, error)
	Detach(ctx context.Context, ebsUUID string) error
	Expand(ctx context.Context, ebsUUID string, sizeGB int64) error
}

type ebsClient struct {
	cli compute.EbsClient
	helper
}

var _ EbsClient = (*ebsClient)(nil)

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

func (t *ebsClient) Get(ctx context.Context, ebsUUID string) (*compute.EbsInfo, error) {
	klog.V(4).Infof("get ebs %s", ebsUUID)
	resp, e := t.cli.GetEbsByUuid(ctx, &compute.GetEbsByUuidRequest{
		EbsUuid: ebsUUID,
	})
	if e != nil {
		return nil, fmt.Errorf("get ebs by uuid: %w", e)
	}
	if resp.Error.Errno != 0 {
		return nil, fmt.Errorf("get ebs by uuid error %s (%d)", resp.Error.Errmsg, resp.Error.Errno)
	}

	infos := resp.GetData()
	if len(infos) == 0 {
		return nil, fmt.Errorf("get ebs by uuid, got nothing")
	}
	if len(infos) > 1 {
		return nil, fmt.Errorf("get ebs by uuid, got too much: %v", infos)
	}

	return infos[0], nil
}

func (t *ebsClient) attachedDevice(ctx context.Context, ebsUUID, dc2Ip string) (string, error) {
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
	if dc2Ip != "" && dc2.GetIp() != dc2Ip {
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
		if job.Result == ebsNotFoundMsg {
			return fmt.Errorf("failed to delete ebs: %w", NotFound)
		}
		return fmt.Errorf("failed to delete ebs: %s", job.Result)
	}
	return nil
}

func (t *ebsClient) Attach(ctx context.Context, ebsUUID, dc2Ip string) (string, error) {
	klog.V(4).Infof("attaching ebs %s to dc2 %s", ebsUUID, dc2Ip)
	// dc2UUID, e := t.getDc2UUIDByName(ctx, dc2Name)
	dc2UUID, e := t.getDc2UUIDByIp(ctx, dc2Ip)
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
	// now check based on getIp ?
	device, e := t.attachedDevice(ctx, ebsUUID, dc2Ip)
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

func (t *ebsClient) Expand(ctx context.Context, ebsUUID string, sizeGB int64) error {
	klog.V(4).Infof("expanding ebs %s", ebsUUID)

	// get to check size
	ebsResp, e := t.cli.GetEbsByUuid(ctx, &compute.GetEbsByUuidRequest{EbsUuid: ebsUUID})
	if e != nil {
		return fmt.Errorf("get ebs error %w", e)
	}
	if ebsResp.Error.Errno != 0 {
		return fmt.Errorf("get ebs error %s (%d)", ebsResp.Error.Errmsg, ebsResp.Error.Errno)
	}
	if len(ebsResp.Data) == 0 {
		klog.V(4).Infof("ebs %s not found", ebsUUID)
		return nil
	}
	curSize := ebsResp.Data[0].GetSize()
	if sizeGB<<30 == curSize {
		klog.V(4).Infof("not expand due to same size %d GiB", sizeGB)
		return nil
	}
	if sizeGB<<30 < curSize {
		return fmt.Errorf("can not shrink size from %d", curSize)
	}

	req := &compute.ChangeEbsSizeRequest{
		Ebs: []*compute.ChangeEbsSizeRequest_Input{{EbsUuid: ebsUUID, Size: sizeGB}},
	}
	resp, e := t.cli.ChangeEbsSize(ctx, req)
	if e != nil {
		return fmt.Errorf("expand ebs error %w", e)
	}
	if resp.Error.Errno != 0 {
		return fmt.Errorf("expand ebs error %s (%d)", resp.Error.Errmsg, resp.Error.Errno)
	}

	job, e := t.waitForJob(ctx, resp.Data[0], "", "")
	if e != nil {
		return e
	}
	if !job.Success {
		return fmt.Errorf("failed to expand ebs: %s", job.Result)
	}
	return nil
}
