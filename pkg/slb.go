package pkg

import (
	"context"
	"errors"
	"fmt"

	"github.com/didiyun/didiyun-go-sdk/base/v1"
	"github.com/didiyun/didiyun-go-sdk/compute/v1"
	"k8s.io/klog"
)

const (
	maxSlbListeners  = 100
	defaultAlgorithm = "wrr"
)

var (
	defaultMonitorInfo = compute.MonitorInputInfo{
		Interval:           10,
		Timeout:            5,
		UnhealthyThreshold: 3,
		HealthyThreshold:   3,
	}
)

type SlbClient interface {
	Create(ctx context.Context, regionID, zoneID, name string, bandwidth int64) (string, error)
	GetExternalIP(ctx context.Context, uuid string) (string, error)
	Delete(ctx context.Context, uuid string) error
	SyncListeners(ctx context.Context, uuid string, listeners []*Listener, dc2Names []string) error
	SyncListenerMembers(ctx context.Context, uuid string, dc2Names []string) error
}

type slbClient struct {
	cli     compute.SLBClient
	vpcUuid string
	helper
}

func (t *slbClient) Create(ctx context.Context, regionID, zoneID, name string, bandwidth int64) (string, error) {
	klog.V(4).Infof("creating slb %s", name)
	req := &compute.CreateSLBRequest{
		Header:       &base.Header{RegionId: regionID, ZoneId: zoneID},
		Count:        1,
		AutoContinue: false,
		PayPeriod:    0,
		Name:         name,
		VpcUuid:      t.vpcUuid,
		AddressType:  "internet",
		Eip: &compute.CreateSLBRequest_Eip{
			Name:      name,
			Bandwidth: bandwidth, // Mbps
		},
	}
	resp, e := t.cli.CreateSLB(ctx, req)
	if e != nil {
		return "", fmt.Errorf("create slb error %w", e)
	}
	if resp.Error.Errno != 0 {
		return "", fmt.Errorf("create slb error %s (%d)", resp.Error.Errmsg, resp.Error.Errno)
	}

	job, e := t.waitForJob(ctx, resp.Data[0], regionID, zoneID)
	if e != nil {
		return "", e
	}
	if !job.Success {
		if job.ResourceUuid != "" { // not success, but still got uuid that already created
			return job.ResourceUuid, nil
		}
		return "", fmt.Errorf("failed to create slb: %s", job.Result)
	}
	return job.ResourceUuid, nil
}

func (t *slbClient) GetExternalIP(ctx context.Context, uuid string) (string, error) {
	klog.V(4).Infof("getting external ip of slb uuid %s", uuid)
	req := &compute.GetSLBByUuidRequest{
		SlbUuid: uuid,
	}
	resp, e := t.cli.GetSLBByUuid(ctx, req)
	if e != nil {
		return "", fmt.Errorf("get slb error %w", e)
	}
	if resp.Error.Errno != 0 {
		return "", fmt.Errorf("get slb error %s (%d)", resp.Error.Errmsg, resp.Error.Errno)
	}
	return resp.Data[0].Beip.Ip, nil
}

func (t *slbClient) Delete(ctx context.Context, uuid string) error {
	klog.V(4).Infof("deleting slb %s", uuid)
	req := &compute.DeleteSLBRequest{
		Slb: []*compute.DeleteSLBRequest_Slb{{SlbUuid: uuid}},
	}
	resp, e := t.cli.DeleteSLB(ctx, req)
	if e != nil {
		return fmt.Errorf("delete slb error %w", e)
	}
	if resp.Error.Errno != 0 {
		return fmt.Errorf("delete slb error %s (%d)", resp.Error.Errmsg, resp.Error.Errno)
	}

	job, e := t.waitForJob(ctx, resp.Data[0], "", "")
	if e != nil {
		return e
	}
	if !job.Success { // not found etc.
		if job.Result == slbNotFoundMsg {
			return fmt.Errorf("failed to delete slb: %w", NotFound)
		}
		return fmt.Errorf("failed to delete slb: %s", job.Result)
	}
	return nil
}

type Listener struct {
	Name     string
	SlbPort  int64
	Dc2Port  int64
	Protocol string
	Uuid     string
}

func (t *slbClient) SyncListeners(ctx context.Context, uuid string, listeners []*Listener, dc2Names []string) error {
	if uuid == "" { // if uuid is empty, all slb will be listed which is not expected
		return errors.New("empty slb uuid")
	}

	klog.V(4).Infof("syncing listeners of slb %s", uuid)
	req := &compute.ListSLBListenerRequest{
		Start:     0,
		Limit:     maxSlbListeners,
		Condition: &compute.ListSLBListenerRequest_Condition{SlbUuid: uuid},
	}
	resp, e := t.cli.ListSLBListener(ctx, req)
	if e != nil {
		return fmt.Errorf("list listeners of slb error %w", e)
	}
	if resp.Error.Errno != 0 {
		return fmt.Errorf("list listeners of slb error %s (%d)", resp.Error.Errmsg, resp.Error.Errno)
	}

	existLis := make(map[string]*compute.ListSLBListenerResponse_Data, len(resp.Data))
	for _, l := range resp.Data {
		existLis[l.Name] = l
	}

	var createLis []*Listener
	var updateLis []*Listener
	var deleteLis []*Listener
	for _, l := range listeners { // empty listeners will remove all existing listeners
		if l.Name == "" {
			klog.V(3).Infof("skip creating lb listener with empty name, uuid %s", uuid)
			continue
		}

		target, ok := existLis[l.Name]
		if ok { // update
			l.Uuid = target.SlbListenerUuid

			// all members ports are same
			if len(target.MemberPorts) > 0 && l.Dc2Port != target.MemberPorts[0] {
				// cuz all members of listener need update, delete & create for updating
				deleteLis = append(deleteLis, &Listener{Uuid: l.Uuid})
				createLis = append(createLis, l)
			} else if l.SlbPort != target.ListenerPort || l.Protocol != target.Protocol {
				updateLis = append(updateLis, l)
			}
		} else { // create
			createLis = append(createLis, l)
		}
		delete(existLis, l.Name)
	}
	// delete left
	for _, l := range existLis {
		deleteLis = append(deleteLis, &Listener{Uuid: l.SlbListenerUuid})
	}

	if e := t.deleteListeners(ctx, deleteLis); e != nil {
		return e
	}

	if len(createLis) > 0 {
		dc2Uuids, e := t.getDc2UUIDsByNames(ctx, t.vpcUuid, dc2Names)
		if e != nil {
			return e
		}

		if e := t.createListeners(ctx, uuid, createLis, dc2Uuids); e != nil {
			return e
		}
	}

	if e := t.updateListeners(ctx, updateLis); e != nil {
		return e
	}

	return nil
}

func (t *slbClient) createListeners(ctx context.Context, uuid string, listeners []*Listener, dc2Members []string) error {
	if len(listeners) == 0 {
		return nil
	}

	klog.V(4).Infof("creating listeners of slb %s", uuid)
	req := &compute.CreateSLBListenerRequest{
		SlbUuid: uuid,
	}
	for _, l := range listeners {
		var memb []*compute.MemberInputInfo
		for _, m := range dc2Members {
			memb = append(memb, &compute.MemberInputInfo{Dc2Uuid: m, Port: l.Dc2Port, Weight: 100})
		}
		req.SlbListener = append(req.SlbListener, &compute.ListenerInputInfo{
			Name:         l.Name,
			Protocol:     l.Protocol,
			BackProtocol: l.Protocol,
			ListenerPort: l.SlbPort,
			Algorithm:    defaultAlgorithm,
			Members:      memb,
			Monitor: &compute.MonitorInputInfo{
				Protocol:           l.Protocol,
				Interval:           defaultMonitorInfo.Interval,
				Timeout:            defaultMonitorInfo.Timeout,
				UnhealthyThreshold: defaultMonitorInfo.UnhealthyThreshold,
				HealthyThreshold:   defaultMonitorInfo.HealthyThreshold,
			},
		})
	}
	resp, e := t.cli.CreateSLBListener(ctx, req)
	if e != nil {
		return fmt.Errorf("create listeners of slb error %w", e)
	}
	if resp.Error.Errno != 0 {
		return fmt.Errorf("create listeners of slb error %s (%d)", resp.Error.Errmsg, resp.Error.Errno)
	}

	job, e := t.waitForJob(ctx, resp.Data, "", "")
	if e != nil {
		return e
	}
	if !job.Success {
		return fmt.Errorf("failed to create listeners of slb: %s", job.Result)
	}
	return nil
}

func (t *slbClient) updateListeners(ctx context.Context, listeners []*Listener) error {
	if len(listeners) == 0 {
		return nil
	}

	klog.V(4).Infof("updating listeners of slb")
	req := &compute.UpdateSLBListenerRequest{}
	for _, l := range listeners {
		req.SlbListener = append(req.SlbListener, &compute.ListenerInput{
			SlbListenerUuid: l.Uuid,
			Name:            l.Name,
			Protocol:        l.Protocol,
			BackProtocol:    l.Protocol,
			ListenerPort:    l.SlbPort,
			Algorithm:       defaultAlgorithm,
			Monitor: &compute.MonitorInputInfo{
				Protocol:           l.Protocol,
				Interval:           defaultMonitorInfo.Interval,
				Timeout:            defaultMonitorInfo.Timeout,
				UnhealthyThreshold: defaultMonitorInfo.UnhealthyThreshold,
				HealthyThreshold:   defaultMonitorInfo.HealthyThreshold,
			},
		})
	}
	resp, e := t.cli.UpdateSLBListener(ctx, req)
	if e != nil {
		return fmt.Errorf("update listeners of slb error %w", e)
	}
	if resp.Error.Errno != 0 {
		return fmt.Errorf("update listeners of slb error %s (%d)", resp.Error.Errmsg, resp.Error.Errno)
	}

	job, e := t.waitForJob(ctx, resp.Data[0], "", "")
	if e != nil {
		return e
	}
	if !job.Success {
		return fmt.Errorf("failed to update listeners of slb: %s", job.Result)
	}
	return nil
}

func (t *slbClient) deleteListeners(ctx context.Context, listeners []*Listener) error {
	if len(listeners) == 0 {
		return nil
	}

	klog.V(4).Infof("deleting listeners of slb")
	req := &compute.DeleteSLBListenerRequest{}
	for _, l := range listeners {
		req.SlbListener = append(req.SlbListener, &compute.DeleteSLBListenerRequest_SlbListener{
			SlbListenerUuid: l.Uuid,
		})
	}
	resp, e := t.cli.DeleteSLBListener(ctx, req)
	if e != nil {
		return fmt.Errorf("delete listeners of slb error %w", e)
	}
	if resp.Error.Errno != 0 {
		return fmt.Errorf("delete listeners of slb error %s (%d)", resp.Error.Errmsg, resp.Error.Errno)
	}

	job, e := t.waitForJob(ctx, resp.Data[0], "", "")
	if e != nil {
		return e
	}
	if !job.Success {
		return fmt.Errorf("failed to delete listeners of slb: %s", job.Result)
	}
	return nil
}

func (t *slbClient) SyncListenerMembers(ctx context.Context, uuid string, dc2Names []string) error {
	if uuid == "" { // if uuid is empty, all slb will be listed which is not expected
		return errors.New("empty slb uuid")
	}

	klog.V(4).Infof("syncing listener members of slb %s", uuid)
	dc2Uuids, e := t.getDc2UUIDsByNames(ctx, t.vpcUuid, dc2Names)
	if e != nil {
		return e
	}

	req := &compute.ListSLBListenerRequest{
		Start:     0,
		Limit:     maxSlbListeners,
		Condition: &compute.ListSLBListenerRequest_Condition{SlbUuid: uuid},
	}
	resp, e := t.cli.ListSLBListener(ctx, req)
	if e != nil {
		return fmt.Errorf("list listeners of slb error %w", e)
	}
	if resp.Error.Errno != 0 {
		return fmt.Errorf("list listeners of slb error %s (%d)", resp.Error.Errmsg, resp.Error.Errno)
	}

	for _, l := range resp.Data {
		req := &compute.ListPoolMembersRequest{
			Start:     0,
			Limit:     maxDc2,
			Condition: &compute.ListPoolMembersRequest_Condition{PoolUuid: l.PoolUuid},
		}
		respMem, e := t.cli.ListPoolMembers(ctx, req)
		if e != nil {
			return fmt.Errorf("list pool members of slb listener error %w", e)
		}
		if respMem.Error.Errno != 0 {
			return fmt.Errorf("list pool members of slb listener error %s (%d)", resp.Error.Errmsg, resp.Error.Errno)
		}

		existMem := make(map[string]*compute.PoolMemberInfo, len(respMem.Data))
		for _, m := range respMem.Data {
			existMem[m.Dc2.Dc2Uuid] = m
		}

		var createMem []string       // dc2 uuid
		var deleteMem []string       // slb mem uuid
		for _, n := range dc2Uuids { // empty dc2Uuids will remove all existing members
			_, ok := existMem[n]
			if !ok {
				createMem = append(createMem, n)
			}
			delete(existMem, n)
		}
		for _, m := range existMem {
			deleteMem = append(deleteMem, m.SlbMemberUuid)
		}

		if len(l.MemberPorts) > 0 {
			if e := t.addListenerMembers(ctx, l.PoolUuid, createMem, l.MemberPorts[0]); e != nil {
				return e
			}
		} else { // no previous members, skip this time
			klog.V(3).Infof("empty members ports, skip adding pool members of listener %s of slb %s", l.Name, uuid)
		}

		if e := t.deleteListenerMembers(ctx, deleteMem); e != nil {
			return e
		}
	}
	return nil
}

func (t *slbClient) addListenerMembers(ctx context.Context, poolUuid string, dc2Uuid []string, port int64) error {
	if len(dc2Uuid) == 0 {
		return nil
	}

	klog.V(4).Infof("adding members of slb pool %s", poolUuid)
	req := &compute.AddSLBMemberToPoolRequest{
		PoolUuid: poolUuid,
	}
	for _, m := range dc2Uuid {
		req.Members = append(req.Members, &compute.MemberInputInfo{
			Dc2Uuid: m,
			Port:    port,
			Weight:  100,
		})
	}
	resp, e := t.cli.AddSLBMemberToPool(ctx, req)
	if e != nil {
		return fmt.Errorf("add members of slb pool error %w", e)
	}
	if resp.Error.Errno != 0 {
		return fmt.Errorf("add members of slb pool error %s (%d)", resp.Error.Errmsg, resp.Error.Errno)
	}

	job, e := t.waitForJob(ctx, resp.Data[0], "", "")
	if e != nil {
		return e
	}
	if !job.Success {
		return fmt.Errorf("failed to add members of slb pool: %s", job.Result)
	}
	return nil
}

func (t *slbClient) deleteListenerMembers(ctx context.Context, memUuid []string) error {
	if len(memUuid) == 0 {
		return nil
	}

	klog.V(4).Infof("deleting members of slb pool")
	req := &compute.DeleteSLBMemberRequest{}
	for _, m := range memUuid {
		req.Members = append(req.Members, &compute.DeleteSLBMemberRequest_Member{
			SlbMemberUuid: m,
		})
	}
	resp, e := t.cli.DeleteSLBMember(ctx, req)
	if e != nil {
		return fmt.Errorf("delete members of slb pool error %w", e)
	}
	if resp.Error.Errno != 0 {
		return fmt.Errorf("delete members of slb pool error %s (%d)", resp.Error.Errmsg, resp.Error.Errno)
	}

	job, e := t.waitForJob(ctx, resp.Data[0], "", "")
	if e != nil {
		return e
	}
	if !job.Success {
		return fmt.Errorf("failed to delete members of slb pool: %s", job.Result)
	}
	return nil
}
