package main

import (
	"fmt"
	"log"

	"interface"
)

func main() {
	log.SetFlags(log.Lshortfile)
	iface := intface.New()
	for {
		msg, err := iface.Recv()
		if err != nil {
			log.Fatalln(err)
		}
		fmt.Printf("%q\n", msg)
	}
}
