package main

import (
	"fmt"
)

func heilhitler() {
	arra := []int{1, 8, 4, 7, 0, 3, 5, 2}
	fmt.Println(arra)
	max := arra[0]
	min := arra[0]
	for i := 0; i < len(arra); i++ {
		fmt.Println(arra[i])
		if arra[i] > max {
			max = arra[i]
		}
		if arra[i] < min {
			min = arra[i]
		}
	}
	fmt.Println(max, min)
}
