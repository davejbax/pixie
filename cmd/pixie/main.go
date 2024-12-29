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

	modlistFile, err := os.Open("moddep.lst")
	if err != nil {
		log.Fatal(err)
	}

	modlist, err := grub.NewDependencyList(modlistFile)
	if err != nil {
		log.Fatal(err)
	}

	modNames, err := modlist.Resolve([]string{
		"normal", "tftp", "http",
	})
	if err != nil {
		log.Fatal(err)
	}

	var mods []*grub.Module

	for _, modName := range modNames {
		mod, err := grub.NewModuleFromDirectory("/usr/lib/grub/x86_64-efi", modName)
		if err != nil {
			log.Fatalf("failed to load module '%s': %v", modName, err)
		}

		slog.Info("adding module", "mod", modName)

		mods = append(mods, mod)
	}

	mods = append(mods, grub.NewPrefixModule("GRUB"))

	img, err := grub.NewImage(f, mods, 4096, 4096)
	if err != nil {
		log.Fatal(err)
	}

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
}
