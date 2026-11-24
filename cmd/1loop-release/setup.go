// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/binary"
	"flag"
	"os"

	"github.com/klauspost/compress/zstd"
)

// setup puts the app at the end of the Windows setup, as cmd/1loop-setup
// (payload.go) reads it: the zstd, its size, the app's, a magic.
func setup(args []string) {
	flags := flag.NewFlagSet("setup", flag.ExitOnError)
	program := flags.String("program", "", "1loop-setup, built")
	app := flags.String("app", "", "the app to install")
	out := flags.String("o", "", "the setup to write")
	flags.Parse(args)
	if *program == "" || *app == "" || *out == "" || flags.NArg() != 0 {
		fail("usage: 1loop-release setup -program FILE.exe -app 1loop.exe -o FILE.exe")
	}
	data := must(os.ReadFile(*app))
	packed := must(packZstd(data))
	tail := struct {
		Packed, App uint64
		Magic       [14]byte
	}{Packed: uint64(len(packed)), App: uint64(len(data))}
	copy(tail.Magic[:], "1loop setup 1\n")
	file := must(os.Create(*out))
	for _, part := range [][]byte{must(os.ReadFile(*program)), packed} {
		must(file.Write(part))
	}
	if err := binary.Write(file, binary.LittleEndian, tail); err != nil {
		fail(err.Error())
	}
	if err := file.Close(); err != nil {
		fail(err.Error())
	}
}

// packZstd is fast to open, where xz took half a minute on a slow PC.
func packZstd(data []byte) ([]byte, error) {
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedBestCompression), zstd.WithWindowSize(1<<26))
	if err != nil {
		return nil, err
	}
	defer encoder.Close()
	return encoder.EncodeAll(data, nil), nil
}
