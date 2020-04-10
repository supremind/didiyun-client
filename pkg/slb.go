package pkg

import (
	"context"
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
	CheckExistence(ctx context.Context, uuid string) (bool, error)
	Delete(ctx context.Context, uuid string) error
	SyncListeners(ctx context.Context, uuid string, listeners []*Listener, dc2Names []string) error
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

func (t *slbClient) CheckExistence(ctx context.Context, uuid string) (bool, error) {
	klog.V(4).Infof("checking slb uuid %s", uuid)
	req := &compute.GetSLBByUuidRequest{
		SlbUuid: uuid,
	}
	resp, e := t.cli.GetSLBByUuid(ctx, req)
	if e != nil {
		return false, fmt.Errorf("get slb error %w", e)
	}
	if resp.Error.Errno != 0 {
		if resp.Error.Errno == 4000 { // TODO: set not found code
			return false, nil
		}
		return false, fmt.Errorf("get slb error %s (%d)", resp.Error.Errmsg, resp.Error.Errno)
	}
	return true, nil
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
		fmt.Printf("%+v\n", *l)
		existLis[l.Name] = l
	}

	var createLis []*Listener
	var updateLis []*Listener
	var deleteLis []*Listener
	for _, l := range listeners {
		target, ok := existLis[l.Name]
		if ok { // update
			l.Uuid = target.SlbListenerUuid

			// all members ports is same
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

	if len(createLis) > 0 {
		dc2List, e := t.listDc2(ctx, t.vpcUuid)
		if e != nil {
			return e
		}
		var dc2Uuids []string
		name2Uuid := make(map[string]string, len(dc2List))
		for _, n := range dc2List {
			name2Uuid[n.GetName()] = n.GetDc2Uuid()
		}
		for _, m := range dc2Names {
			id, ok := name2Uuid[m]
			if ok {
				dc2Uuids = append(dc2Uuids, id)
			}
		}

		if e := t.createListeners(ctx, uuid, createLis, dc2Uuids); e != nil {
			return e
		}
	}

	if e := t.updateListeners(ctx, updateLis); e != nil {
		return e
	}

	if e := t.deleteListeners(ctx, deleteLis); e != nil {
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
