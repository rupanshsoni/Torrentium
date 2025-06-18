package main

import (
	"fmt"
)

func doosra_ques() string {
	var n int
	fmt.Scan(&n)
	arr := make([]int, n)
	for i := 0; i < n; i++ {
		fmt.Scan(&arr[i])
	}
	var num int
	fmt.Scan(&num)
	for j := 0; j < n; j++ {
		if arr[j] == num {
			return "exists"
		}
	}
	return "not exists"
}
