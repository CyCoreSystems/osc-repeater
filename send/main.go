package main

import (
	"flag"
	"log"

	"github.com/hypebeast/go-osc/osc"
)

var targetPort int

func init() {
	flag.IntVar(&targetPort, "p", 8080, "target port")
}

func main() {
	flag.Parse()

	c := osc.NewClient("127.0.0.1", targetPort)

	m := osc.NewMessage("/test", int32(101))

	if err := c.Send(m); err != nil {
		log.Println("failed to send message:", err)
	}
}

