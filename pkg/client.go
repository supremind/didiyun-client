package pkg

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"time"

	"github.com/didiyun/didiyun-go-sdk/base/v1"
	"github.com/didiyun/didiyun-go-sdk/compute/v1"
	"golang.org/x/oauth2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/oauth"
	"k8s.io/klog"
)

const (
	endpoint     = "open.didiyunapi.com:8080"
	pollInterval = 3 * time.Second
	maxDc2       = 500
	maxSlb       = 1000

	ebsNotFoundMsg  = "找不到指定EBS"
	slbNotFoundMsg  = "找不到指定SLB"
	slbNotFoundCode = 41070 // 查询SLB信息失败
)

var (
	NotFound = errors.New("not found")
)

type Client interface {
	Ebs() EbsClient
	Slb(vpcUuid string) SlbClient
}

type Config struct {
	Token   string
	Timeout time.Duration
}

type client struct {
	conn *grpc.ClientConn

	// for helper
	job compute.CommonClient
	dc2 compute.Dc2Client
}

func New(cfg *Config) (Client, error) {
	cred := oauth.NewOauthAccess(&oauth2.Token{
		AccessToken: cfg.Token,
		TokenType:   "bearer",
	})
	conn, e := grpc.Dial(endpoint,
		grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{})),
		grpc.WithPerRPCCredentials(cred),
		grpc.WithTimeout(cfg.Timeout),
	)
	if e != nil {
		return nil, e
	}
	return &client{
		conn: conn,
		job:  compute.NewCommonClient(conn),
		dc2:  compute.NewDc2Client(conn),
	}, nil
}

func (t *client) Close() error {
	return t.conn.Close()
}

func (t *client) Ebs() EbsClient {
	return &ebsClient{
		cli:    compute.NewEbsClient(t.conn),
		helper: t,
	}
}

func (t *client) Slb(vpcUuid string) SlbClient {
	return &slbClient{
		cli:     compute.NewSLBClient(t.conn),
		vpcUuid: vpcUuid,
		helper:  t,
	}
}

type helper interface {
	getDc2UUIDByName(ctx context.Context, name string) (string, error)
	getDc2UUIDsByNames(ctx context.Context, vpcUuid string, names []string) ([]string, error)
	waitForJob(ctx context.Context, info *base.JobInfo, regionID, zoneID string) (*base.JobInfo, error)
}

func (t *client) getDc2UUIDByName(ctx context.Context, name string) (string, error) {
	klog.V(4).Infof("getting dc2 uuid by %s", name)
	req := &compute.ListDc2Request{
		Start:     0,
		Limit:     maxDc2,
		Simplify:  true,
		Condition: &compute.ListDc2Condition{Dc2Name: name},
	}
	resp, e := t.dc2.ListDc2(ctx, req)
	if e != nil {
		return "", fmt.Errorf("get dc2 error %w", e)
	}
	if resp.Error.Errno != 0 {
		return "", fmt.Errorf("get dc2 error %s (%d)", resp.Error.Errmsg, resp.Error.Errno)
	}
	for _, d := range resp.Data {
		if d.GetName() == name {
			return d.GetDc2Uuid(), nil
		}
	}
	return "", fmt.Errorf("dc2 %s is not found", name)
}

func (t *client) getDc2UUIDsByNames(ctx context.Context, vpcUuid string, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}

	klog.V(4).Infof("getting dc2 uuids by names")
	req := &compute.ListDc2Request{
		Start:    0,
		Limit:    maxDc2,
		Simplify: true,
	}
	if vpcUuid != "" {
		req.Condition = &compute.ListDc2Condition{VpcUuids: []string{vpcUuid}}
	}
	resp, e := t.dc2.ListDc2(ctx, req)
	if e != nil {
		return nil, fmt.Errorf("list dc2 error %w", e)
	}
	if resp.Error.Errno != 0 {
		return nil, fmt.Errorf("list dc2 error %s (%d)", resp.Error.Errmsg, resp.Error.Errno)
	}

	var dc2Uuids []string
	name2Uuid := make(map[string]string, len(resp.Data))
	for _, n := range resp.Data {
		name2Uuid[n.GetName()] = n.GetDc2Uuid()
	}
	for _, m := range names {
		id, ok := name2Uuid[m]
		if ok {
			dc2Uuids = append(dc2Uuids, id)
		}
	}
	return dc2Uuids, nil
}

func (t *client) waitForJob(ctx context.Context, info *base.JobInfo, regionID, zoneID string) (*base.JobInfo, error) {
	for {
		if info.Done {
			return info, nil
		}

		klog.V(5).Infof("wait for job %+v", *info)
		time.Sleep(pollInterval) // simply use a constant interval
		resp, e := t.job.JobResult(ctx, &compute.JobResultRequest{
			Header:   &base.Header{RegionId: regionID, ZoneId: zoneID},
			JobUuids: []string{info.JobUuid},
		})
		if e != nil {
			return nil, fmt.Errorf("job result error %w", e)
		}
		if resp.Error.Errno != 0 {
			return nil, fmt.Errorf("job result error %s (%d)", resp.Error.Errmsg, resp.Error.Errno)
		}
		info = resp.Data[0]
	}
}
