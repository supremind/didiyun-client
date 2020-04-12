package example

import (
	"flag"

	"k8s.io/klog"
)

const (
	// update your api token, etc.
	apiToken = ""
	vpcUuid  = "cbd25af2620c4089a382b3ad1bb55d50"
)

func init() {
	klogFlags := flag.NewFlagSet("klog", flag.ExitOnError)
	klog.InitFlags(klogFlags)
	klogFlags.Set("v", "5")
}
