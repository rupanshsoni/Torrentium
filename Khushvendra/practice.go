// Arrays & Counting : 
//Sum of Elements - Calculate and print the sum of all numbers in an array.

// Count Even Numbers - Count how many even numbers are present in the array.

// Count Odd Numbers - Count how many odd numbers are present in the array.

package main

import "fmt"

func main1() {
	var arr [10]int = [10]int{1,2,3,4,5,6,7,8,9,10}
	sum := 0

	for i:=0; i<10; i++ {
		sum = sum + arr[i]
	}
	fmt.Println("sum of array :",sum)

	isEven := 0

	for j:=0; j<10; j++ {
		if arr[j] % 2 == 0 {
			isEven ++
		}
	}
	fmt.Println("even elements :", isEven)

	isOdd := 0
	for k:=0; k<10; k++ {
		if arr[k] % 2 == 0 {
			isOdd ++
		}
	}
	fmt.Println("odd elements :", isOdd)
}

