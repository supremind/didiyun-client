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
	id, e := slb.Create(ctx, "gz", "gz02", "ExampleCreateDelete_Slb", 2) // name is not unique
	if e != nil {
		log.Fatalln(e)
	}

	if _, e = slb.GetExternalIP(ctx, id); e != nil {
		log.Fatalln(e)
	}

	if e = slb.Delete(ctx, id); e != nil {
		log.Fatalln(e)
	}

	fmt.Println("Slb created & deleted ok")
	// Output: Slb created & deleted ok
}

func ExampleSyncListeners_Slb() {
	ctx := context.Background()
	c, e := pkg.New(&pkg.Config{Token: apiToken, Timeout: 5 * time.Second})
	if e != nil {
		log.Fatalln(e)
	}
	slb := c.Slb(vpcUuid)

	id, e := slb.Create(ctx, "gz", "gz02", "ExampleSyncListeners_Slb", 2) // name is not unique
	if e != nil {
		log.Fatalln(e)
	}
	defer func() {
		if e = slb.Delete(ctx, id); e != nil {
			log.Fatalln(e)
		}
	}()

	listeners := []*pkg.Listener{
		{Name: "http", SlbPort: 5090, Dc2Port: 5092, Protocol: "TCP"},
		{Name: "rtmp", SlbPort: 5080, Dc2Port: 5082, Protocol: "TCP"},
	}
	if e := slb.SyncListeners(ctx, id, listeners, []string{"atom9", "atom8"}); e != nil {
		log.Fatalln(e)
	}

	fmt.Println("Slb listeners synced ok")
	// Output: Slb listeners synced ok
}

func ExampleSyncListenerMembers_Slb() {
	ctx := context.Background()
	c, e := pkg.New(&pkg.Config{Token: apiToken, Timeout: 5 * time.Second})
	if e != nil {
		log.Fatalln(e)
	}
	slb := c.Slb(vpcUuid)

	id, e := slb.Create(ctx, "gz", "gz02", "ExampleSyncListenerMembers_Slb", 2) // name is not unique
	if e != nil {
		log.Fatalln(e)
	}
	defer func() {
		if e = slb.Delete(ctx, id); e != nil {
			log.Fatalln(e)
		}
	}()

	if e := slb.SyncListenerMembers(ctx, id, []string{"atom7", "atom8"}); e != nil {
		log.Fatalln(e)
	}

	fmt.Println("Slb listener members synced ok")
	// Output: Slb listener members synced ok
}
