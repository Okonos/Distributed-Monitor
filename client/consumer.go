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
		logger.Println("Consuming value")
		val := monitor.Get()
		logger.Println("Consumed value", val)
		sleepDuration := time.Duration((rng.Intn(15) + 5) * 500)
		time.Sleep(sleepDuration * time.Millisecond)
	}
}
