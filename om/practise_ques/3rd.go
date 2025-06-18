package main

import (
	"fmt"
)

func teesra_ques() int {
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
			return j
		}
	}
	return -1
}
