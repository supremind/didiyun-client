package pkg

import (
	"context"
	"crypto/tls"
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
)

type Client interface {
	Ebs() EbsClient
	Elb() ElbClient
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

func (t *client) Elb() ElbClient {
	return &elbClient{
		cli:    compute.NewCommonClient(t.conn),
		helper: t,
	}
}

type helper interface {
	getDc2UUIDByName(ctx context.Context, name string) (string, error)
	waitForJob(ctx context.Context, info *base.JobInfo, regionID, zoneID string) (*base.JobInfo, error)
}

func (t *client) getDc2UUIDByName(ctx context.Context, name string) (string, error) {
	klog.V(4).Infof("getting dc2 uuid by %s", name)
	req := &compute.ListDc2Request{
		Start:     0,
		Limit:     100,
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
