
![GitHub Release](https://img.shields.io/github/v/release/davejbax/pixie)
[![Go Reference](https://pkg.go.dev/badge/github.com/davejbax/pixie.svg)](https://pkg.go.dev/github.com/davejbax/pixie)
[![Go Report Card](https://goreportcard.com/badge/github.com/davejbax/pixie)](https://goreportcard.com/report/github.com/davejbax/pixie)
![Test workflow](https://github.com/davejbax/pixie/actions/workflows/test.yml/badge.svg)

<div align="center">
  <img src="./docs/pixie.png" height="200">
  <h1>Pixie</h1>
  
  <p align="center">
  A PXE boot server written in Go, aiming to make network installations of Linux distributions simple.
  </p>
</div>

## Overview

_(Note: this project is under construction, and does not yet have any releases)_

Pixie aims to provide a lightweight, zero dependency (no system packages required), modern way to support installing Linux on bare metal and virtual machines via UEFI network (PXE) boot.

The main motivation behind this project is to ease the complexity and manual maintenance around this task. Existing solutions for network booting often require many manual steps, such as acquiring and extracting Linux ISOs (and updating them for new OS releases), setting up a TFTP boot directory, installing/running `grub-mknetdir`, setting up an HTTP(S) server with the installation tree and kickstart files, etc.

In contrast, Pixie requires no external packages, and asks users only to write a simple **declarative configuration** file and point a DHCP server at its address. Pixie will handle:

* Generating UEFI bootloaders with GRUB
* Creating and serving necessary files for boot and installation over TFTP/HTTP
* Fetching and extracting Linux ISOs for configured OSes
* Optionally, automatically upgrading OS versions to new minor releases (e.g. Rocky 9.1 -> Rocky 9.4)

## Features

Key:

✅ Supported<br>
🚧 Work in progress<br>
⏳ Planned<br>
❌ Unsupported

<table>
  <tr>
    <th>Feature</th>
    <th>Status</th>
  </tr>
  <tr>
    <td colspan="2" align="center">Boot</td>
  </tr>
  <tr>
    <td>UEFI: x86_64</td>
    <td>✅</td>
  </tr>
  <tr>
    <td>UEFI: ARM64</td>
    <td>🚧</td>
  </tr>
  <tr>
    <td>UEFI: i386</td>
    <td>⏳</td>
  </tr>
  <tr>
    <td>UEFI: other architectures</td>
    <td>❌</td>
  </tr>
  <tr>
    <td>BIOS</td>
    <td>❌</td>
  </tr>
  <tr>
    <td>Bootloader: GRUB</td>
    <td>✅</td>
  </tr>
  <tr>
    <td>Bootloader: OCI image</td>
    <td>⏳</td>
  </tr>
  <tr>
    <td>Bootloader: local UEFI file</td>
    <td>⏳</td>
  </tr>
  <tr>
    <td colspan="2" align="center">Services</td>
  </tr>
  <tr>
    <td>TFTP server</td>
    <td>🚧</td>
  </tr>
  <tr>
    <td>HTTP server</td>
    <td>⏳</td>
  </tr>
  <tr>
    <td>Bootable ISO creation</td>
    <td>🚧</td>
  </tr>
  <tr>
    <td colspan="2" align="center">Operating systems</td>
  </tr>
  <tr>
    <td>Ubuntu</td>
    <td>⏳</td>
  </tr>
  <tr>
    <td>Debian</td>
    <td>⏳</td>
  </tr>
  <tr>
    <td>Rocky Linux</td>
    <td>⏳</td>
  </tr>
  <tr>
    <td>User-provided Linux ISO</td>
    <td>⏳</td>
  </tr>
  <tr>
    <td>Non-Linux OS</td>
    <td>❌</td>
  </tr>
  <tr>
    <td colspan="2" align="center">Deployment</td>
  </tr>
  <tr>
    <td>`go install`</td>
    <td>✅</td>
  </tr>
  <tr>
    <td>Helm chart</td>
    <td>⏳</td>
  </tr>
  <tr>
    <td>OS package</td>
    <td>⏳</td>
  </tr>
</table>