package main

import (
	"log"
	"math/rand"
	"os"
	"time"

	"monitor-interface"
)

func main() {
	src := rand.NewSource(time.Now().UnixNano())
	rng := rand.New(src)

	monitor := monitorInterface.Init()

	id := monitor.GetID()
	logger := log.New(os.Stdout, id[:8]+" ", log.Lmicroseconds)

	for {
		val := rng.Intn(100)
		logger.Println("Produced value:", val)
		monitor.Put(val)
		logger.Println("Put value to buffer")
		sleepDuration := time.Duration((rng.Intn(10) + 1) * 500)
		time.Sleep(sleepDuration * time.Millisecond)
	}
}
