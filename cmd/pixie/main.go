package main

import (
	"fmt"
	"log"
	"os"

	"github.com/davejbax/pixie/internal/grub"
)

func main() {
	f, err := os.Open("kernel.img")
	if err != nil {
		log.Fatal(err)
	}

	img, err := grub.NewImage(f, 512, 4096)
	if err != nil {
		log.Fatal(err)
	}

	buff, err := os.OpenFile("output.bin", os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatal(err)
	}

	written, err := img.WriteTo(buff)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("wrote %d bytes\n", written)

	modlistFile, err := os.Open("moddep.lst")
	if err != nil {
		log.Fatal(err)
	}

	modlist, err := grub.NewDependencyList(modlistFile)
	if err != nil {
		log.Fatal(err)
	}

	mods, err := modlist.Resolve([]string{"font", "time", "lvm"})
	if err != nil {
		log.Fatal(err)
	}

	for _, mod := range mods {
		fmt.Printf("mod: %s\n", mod)
	}
}
