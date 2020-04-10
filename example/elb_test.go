package example

import (
	"context"
	"fmt"
	"log"
	"time"

	"git.supremind.info/products/atom/didiyun-client/pkg"
)

func ExampleCreateDelete_Slb() {
	ctx := context.Background()
	c, e := pkg.New(&pkg.Config{Token: apiToken, Timeout: 5 * time.Second})
	if e != nil {
		log.Fatalln(e)
	}
	slb := c.Slb(vpcUuid)
	id, e := slb.Create(ctx, "gz", "gz02", "Example_Slb", 2) // name is not unique
	if e != nil {
		log.Fatalln(e)
	}

	if e = slb.Delete(ctx, id); e != nil {
		log.Fatalln(e)
	}

	exist, e := slb.CheckExistence(ctx, id)
	if e != nil {
		log.Fatalln(e)
	}
	if exist {
		log.Fatalln("should not be exist")
	}

	fmt.Println("Slb created & deleted ok")
	// Output: Slb created & deleted ok
}

func ExampleSync_Slb() {
	ctx := context.Background()
	c, e := pkg.New(&pkg.Config{Token: apiToken, Timeout: 5 * time.Second})
	if e != nil {
		log.Fatalln(e)
	}
	slb := c.Slb(vpcUuid)

	listeners := []*pkg.Listener{
		{Name: "sip", SlbPort: 5080, Dc2Port: 5080, Protocol: "TCP"},
		{Name: "sip2", SlbPort: 5081, Dc2Port: 5081, Protocol: "TCP"},
	}
	if e := slb.SyncListeners(ctx, "759933c302425dafb188eb0e0d47d6e7", listeners, []string{"atom9"}); e != nil {
		log.Fatalln(e)
	}

	fmt.Println("Slb synced ok")
	// Output: Slb synced ok
}
