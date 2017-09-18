package main

import (
	"log"
	"time"
)

func main() {
	log.SetFlags(log.Lshortfile)
	s := newServer()
	bcastAt := time.Now().Add(2 * time.Second)

	s.loop()

	for {
		now := time.Now()
		if now.After(bcastAt) {
			// iface.Send("BROADCAST", "czesc")
			bcastAt = now.Add(2 * time.Second)
		}

		// msg, err := iface.Recv() // non-blocking
		// if err != nil {
		// 	if err == zmq.AsErrno(syscall.EAGAIN) {
		// 		continue
		// 	} else {
		// 		log.Fatalln(err)
		// 	}
		// }
		// fmt.Println(strings.Join(msg, " "))
	}
}
