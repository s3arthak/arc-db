// Command arcdb is the ArcDB entry point.
//
// Phase 0: it only prints a banner so we can establish the
// build -> test -> commit loop. Real commands arrive with the
// query engine milestone.
package main

import "fmt"

func main() {
	fmt.Println("ArcDB 0.0.0 (phase 0: repository bootstrap)")
}
