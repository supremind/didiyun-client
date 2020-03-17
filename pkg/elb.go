package pkg

import "github.com/didiyun/didiyun-go-sdk/compute/v1"

type ElbClient interface {
}

type elbClient struct {
	cli compute.CommonClient
	helper
}
