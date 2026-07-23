package httpserver

import "fmt"

func brokenVetCheck() {
	fmt.Sprintf("%d", "not a number")
}
