// Command binversion prints the value the linker gave
// internal/version.Version in a built Dorylinae binary (ELF, Mach-O or PE,
// 64-bit little-endian), by reading the symbol table and the string it
// points to. It needs a binary linked WITHOUT -s: -s drops the symbol table.
//
//	go run ./tools/binversion stage/agentnet
//
// The release workflow uses it because `go version -m` cannot show the
// -X value: cmd/go records -ldflags in the build info only when -trimpath is
// off (go.dev/issue/52372), and release builds use -trimpath.
//
// Exit codes: 0 printed, 2 unreadable binary, no symbol table or no such symbol.
package main

import (
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
)

const symbol = "github.com/Magazem/Dorylinae-Agentnet/internal/version.Version"

// maxLen bounds the string read, so a corrupt header cannot allocate much.
const maxLen = 256

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: binversion <binary>")
		os.Exit(2)
	}
	v, err := read(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "binversion:", err)
		os.Exit(2)
	}
	fmt.Println(v)
}

// section is a loaded address range of the binary.
type section struct {
	addr, size uint64
	r          io.ReaderAt
}

// image is the part of an executable binversion needs: where the symbol is
// and what bytes live at a virtual address.
type image struct {
	sym      uint64
	sections []section
}

func (im image) readAt(addr, n uint64) ([]byte, error) {
	for _, s := range im.sections {
		if addr >= s.addr && addr-s.addr <= s.size && n <= s.size-(addr-s.addr) {
			off := addr - s.addr
			// Section sizes come from the file's headers, so a corrupt file
			// can claim offsets no io.ReaderAt can take.
			if off > math.MaxInt64 {
				return nil, fmt.Errorf("address %#x is beyond any readable offset", addr)
			}
			b := make([]byte, n)
			if _, err := s.r.ReadAt(b, int64(off)); err != nil {
				return nil, err
			}
			return b, nil
		}
	}
	return nil, fmt.Errorf("address %#x (+%d) is in no section", addr, n)
}

func read(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // the path is a user-supplied CLI argument, as intended
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	im, err := load(f)
	if err != nil {
		return "", err
	}
	// A Go string is {ptr, len}, two 8-byte words on amd64 and arm64.
	hdr, err := im.readAt(im.sym, 16)
	if err != nil {
		return "", fmt.Errorf("string header: %w", err)
	}
	ptr, n := binary.LittleEndian.Uint64(hdr), binary.LittleEndian.Uint64(hdr[8:])
	if n == 0 || n > maxLen {
		return "", fmt.Errorf("%s has length %d", symbol, n)
	}
	b, err := im.readAt(ptr, n)
	if err != nil {
		return "", fmt.Errorf("string data: %w", err)
	}
	return string(b), nil
}

var errNoSymbol = errors.New("no symbol " + symbol + " (built with -s, or not a Dorylinae binary?)")

func load(f *os.File) (image, error) {
	if e, err := elf.NewFile(f); err == nil {
		return loadELF(e)
	}
	if m, err := macho.NewFile(f); err == nil {
		return loadMachO(m)
	}
	if p, err := pe.NewFile(f); err == nil {
		return loadPE(p)
	}
	return image{}, errors.New("not a 64-bit ELF, Mach-O or PE binary")
}

func loadELF(f *elf.File) (image, error) {
	if f.Class != elf.ELFCLASS64 || f.ByteOrder != binary.LittleEndian {
		return image{}, errors.New("not a 64-bit little-endian ELF")
	}
	syms, err := f.Symbols()
	if err != nil {
		return image{}, fmt.Errorf("%w: %w", errNoSymbol, err)
	}
	im := image{}
	found := false
	for _, s := range syms {
		if s.Name == symbol {
			im.sym, found = s.Value, true
			break
		}
	}
	if !found {
		return image{}, errNoSymbol
	}
	for _, s := range f.Sections {
		if s.Type == elf.SHT_PROGBITS && s.Addr != 0 {
			im.sections = append(im.sections, section{s.Addr, s.Size, s})
		}
	}
	return im, nil
}

func loadMachO(f *macho.File) (image, error) {
	if f.Magic != macho.Magic64 {
		return image{}, errors.New("not a 64-bit Mach-O")
	}
	if f.Symtab == nil {
		return image{}, errNoSymbol
	}
	im := image{}
	found := false
	for _, s := range f.Symtab.Syms {
		if s.Name == symbol || s.Name == "_"+symbol {
			im.sym, found = s.Value, true
			break
		}
	}
	if !found {
		return image{}, errNoSymbol
	}
	for _, s := range f.Sections {
		// Zero-fill sections (bss) have no file bytes to read.
		if t := s.Flags & 0xff; t != 0x1 && t != 0xc && s.Offset != 0 {
			im.sections = append(im.sections, section{s.Addr, s.Size, s})
		}
	}
	return im, nil
}

func loadPE(f *pe.File) (image, error) {
	oh, ok := f.OptionalHeader.(*pe.OptionalHeader64)
	if !ok {
		return image{}, errors.New("not a 64-bit PE")
	}
	im := image{}
	found := false
	for _, s := range f.Symbols {
		if s.Name == symbol && s.SectionNumber > 0 && int(s.SectionNumber) <= len(f.Sections) {
			sec := f.Sections[s.SectionNumber-1]
			im.sym, found = oh.ImageBase+uint64(sec.VirtualAddress)+uint64(s.Value), true
			break
		}
	}
	if !found {
		return image{}, errNoSymbol
	}
	for _, s := range f.Sections {
		// Only the initialised part (SizeOfRawData) is readable from the file.
		size := uint64(s.Size)
		if uint64(s.VirtualSize) < size {
			size = uint64(s.VirtualSize)
		}
		if size > 0 {
			im.sections = append(im.sections, section{oh.ImageBase + uint64(s.VirtualAddress), size, s})
		}
	}
	return im, nil
}
