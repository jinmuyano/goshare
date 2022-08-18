/*
 * @Date: 2022-08-17 17:20:41
 * @LastEditors: zhangwenqiang
 * @LastEditTime: 2022-08-18 18:33:16
 * @FilePath: /test-tunny/main.go
 */
// /*
//  * @Date: 2022-08-17 17:20:41
//  * @LastEditors: zhangwenqiang
//  * @LastEditTime: 2022-08-17 19:05:44
//  * @FilePath: /test/main.go
//  */
// package main

// import (
// 	"io/ioutil"
// 	"net/http"
// 	"runtime"
// 	"test/tunny"
// 	"fmt"
// )

// func main() {
// 	numCPUs := runtime.NumCPU()

// 	pool := tunny.NewFunc(numCPUs, func(payload interface{}) interface{} {
// 		var result []byte

// 		// TODO: Something CPU heavy with payload

// 		return result
// 	})
// 	defer pool.Close()

// 	http.HandleFunc("/work", func(w http.ResponseWriter, r *http.Request) {
// 		input, err := ioutil.ReadAll(r.Body)
// 		if err != nil {
// 			http.Error(w, "Internal error", http.StatusInternalServerError)
// 		}
// 		fmt.Println(input)
// 		defer r.Body.Close()

// 		// Funnel this work into our pool. This call is synchronous and will
// 		// block until the job is completed.
// 		result := pool.Process(input)

// 		w.Write(result.([]byte))
// 	})

// 	http.ListenAndServe(":8080", nil)
// }

package main

import (
	"log"
	"test-tunny/tunny"
	"time"
)

func main() {
	// 创建3000的
	pool := tunny.NewFunc(3000, func(i interface{}) interface{} {
		log.Println(i)
		time.Sleep(time.Second)
		return nil
	})
	defer pool.Close()

	for i := 0; i < 100000; i++ {
		go pool.Process(i) //参数i是传给任务函数的
	}
	time.Sleep(time.Second * 4)
}
