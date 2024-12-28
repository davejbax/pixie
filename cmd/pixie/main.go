package main

import (
	"fmt"
	"log"
	"log/slog"
	"os"

	"github.com/davejbax/pixie/internal/efipe"
	"github.com/davejbax/pixie/internal/grub"
)

func main() {
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
	s := slog.New(handler)
	slog.SetDefault(s)

	f, err := os.Open("kernel.img")
	if err != nil {
		log.Fatal(err)
	}

	// headerSize := 512

	img, err := grub.NewImage(f, []*grub.Module{grub.NewPrefixModule("GRUB")}, 4096, 4096)
	if err != nil {
		log.Fatal(err)
	}

	// sects := img.Sections()
	// sect, ok := sects.GetByName(efipe.SectionData)
	// if !ok {
	// 	log.Fatal("not ok")
	// }

	// r := sect.Open()
	// debug := make([]byte, 16)
	// var read int

	// for err == nil {
	// 	read, err = r.Read(debug)
	// 	for _, byt := range debug {
	// 		fmt.Printf("%02x ", byt)
	// 	}
	// 	fmt.Printf("(%d)\n", read)
	// }

	efiImg, err := efipe.New(img)
	if err != nil {
		log.Fatal(err)
	}

	buff, err := os.OpenFile("output.efi", os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatal(err)
	}

	written, err := efiImg.WriteTo(buff)
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
