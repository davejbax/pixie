package main

import (
	"context"
	"log"

	"github.com/davejbax/pixie/internal/bootloader"
)

func main() {
	_, err := bootloader.LoadGrubOrDownload(context.Background(), bootloader.NewGrubConfig("E:\\Temp\\GrubTest", "2.12"))
	if err != nil {
		log.Fatal(err)
	}
}
