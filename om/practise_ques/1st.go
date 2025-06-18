package main

import (
	"fmt"
)

func pahla_ques() int {
	var n int // LENGTH OF ARRAY
	fmt.Scan(&n)
	arr := make([]int, n) // SLICE BANAYA HAI
	for i := 0; i < n; i++ {
		fmt.Scan(&arr[i]) // elements input
	}
	var count int = 0
	var num int
	fmt.Scan(&num)
	for j := 0; j < n; j++ {
		if arr[j] == num {
			count++
		}
	}
	return count
}
