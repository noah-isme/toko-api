// Command vapid_keygen prints a fresh VAPID key pair for web push, in the
// env-var form the API and worker read. The private key is a secret: store it
// like SMTP_PASSWORD, never commit it.
package main

import (
	"fmt"
	"os"

	webpush "github.com/SherClockHolmes/webpush-go"
)

func main() {
	privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		fmt.Fprintf(os.Stderr, "vapid_keygen: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("PUSH_VAPID_PUBLIC_KEY=%s\n", publicKey)
	fmt.Printf("PUSH_VAPID_PRIVATE_KEY=%s\n", privateKey)
}
