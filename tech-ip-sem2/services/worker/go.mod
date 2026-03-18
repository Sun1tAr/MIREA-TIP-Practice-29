module github.com/sun1tar/MIREA-TIP-Practice-29/tech-ip-sem2/worker

go 1.22

require (
	github.com/rabbitmq/amqp091-go v1.10.0
	github.com/sirupsen/logrus v1.9.4
	github.com/sun1tar/MIREA-TIP-Practice-29/tech-ip-sem2/shared v0.0.0
)

require golang.org/x/sys v0.13.0 // indirect

replace github.com/sun1tar/MIREA-TIP-Practice-29/tech-ip-sem2/shared => ../../shared
