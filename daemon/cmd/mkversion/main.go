// Command mkversion prints linker flags for a versioned Anyscale build.
package main

import (
	"fmt"
	"log"

	"github.com/gabriwar/anyscale/daemon/version/mkversion"
)

func main() {
	info, err := mkversion.InfoFrom(".")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("-X github.com/gabriwar/anyscale/daemon/version.shortStamp=%s ", info.Short)
	fmt.Printf("-X github.com/gabriwar/anyscale/daemon/version.longStamp=%s ", info.Long)
	fmt.Printf("-X github.com/gabriwar/anyscale/daemon/version.gitCommitStamp=%s\n", info.GitHash)
}
