package main

import (
	"fmt"
	"time"

	monitor "monitor-interface"

	"github.com/pborman/uuid"
	zmq "github.com/pebbe/zmq4"
)

func setID(soc *zmq.Socket) {
	identity := uuid.NewRandom().String()
	soc.SetIdentity(identity)
}

func main() {
	m := monitor.Init()
	// m.Check()
	m.Put(7)
	for {
		fmt.Println(m.Get())
		time.Sleep(3 * time.Second)
	}
}
