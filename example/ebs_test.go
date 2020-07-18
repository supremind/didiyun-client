package example

import (
	"context"
	"fmt"
	"log"

	"github.com/supremind/didiyun-client/pkg"
)

func ExampleCreateDeleteExpand_Ebs() {
	ctx := context.Background()
	c, e := pkg.NewMock()
	if e != nil {
		log.Fatalln(e)
	}
	ebs := c.Ebs()
	id, e := ebs.Create(ctx, "gz", "gz02", "ExampleCreateDelete_Ebs", "SSD", 20) // name is not unique
	if e != nil {
		log.Fatalln(e)
	}
	fmt.Println("Ebs created & deleted ok")

	if e = ebs.Delete(ctx, id); e != nil {
		log.Fatalln(e)
	}

	fmt.Println("Ebs expanded ok")
	// Output:
	// Ebs created & deleted ok
	// Ebs expanded ok
}
