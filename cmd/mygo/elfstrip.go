package main

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// stripELF copies the ELF file src to dst without the sections a program
// does not load: debug information and symbol tables, which make up most of
// CEF's Linux libraries (2.2 GB of libcef.so's 2.5 GB on arm64). It does
// what `strip` does, in pure Go, so any system builds Linux apps: the bytes
// the segments load are kept as they are, followed by a new section header
// table of the sections they hold. Files other than 64-bit little-endian
// ELF ones, and ones laid out otherwise, are copied unchanged.
func stripELF(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	f, err := elf.NewFile(in)
	if err != nil || f.Class != elf.ELFCLASS64 || f.Data != elf.ELFDATA2LSB {
		return copyFile(src, dst, info.Mode())
	}
	var end uint64 // the end of what the segments hold
	for _, p := range f.Progs {
		end = max(end, p.Off+p.Filesz)
	}
	// The sections kept, by index in the file, with their new indices.
	newIndex := map[int]int{}
	var kept []*elf.Section
	for i, s := range f.Sections {
		if i == 0 || s.Flags&elf.SHF_ALLOC == 0 {
			continue
		}
		if s.Type != elf.SHT_NOBITS && s.Offset+s.Size > end {
			return copyFile(src, dst, info.Mode()) // not laid out as expected
		}
		kept = append(kept, s)
		newIndex[i] = len(kept)
	}
	if end == 0 || len(kept) == 0 || end >= uint64(info.Size()) {
		return copyFile(src, dst, info.Mode())
	}

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, io.NewSectionReader(in, 0, int64(end))); err != nil {
		return err
	}
	// The names of the sections, then their headers, 8-byte aligned.
	var names bytes.Buffer
	names.WriteByte(0)
	nameOf := func(s string) uint32 {
		off := uint32(names.Len())
		names.WriteString(s)
		names.WriteByte(0)
		return off
	}
	headers := make([]elf.Section64, 1, len(kept)+2)
	for _, s := range kept {
		h := elf.Section64{
			Name: nameOf(s.Name), Type: uint32(s.Type), Flags: uint64(s.Flags), Addr: s.Addr,
			Off: s.Offset, Size: s.Size, Link: s.Link, Info: s.Info, Addralign: s.Addralign, Entsize: s.Entsize,
		}
		h.Link = uint32(newIndex[int(s.Link)]) // 0 for sections not kept
		if s.Type == elf.SHT_REL || s.Type == elf.SHT_RELA || s.Flags&elf.SHF_INFO_LINK != 0 {
			h.Info = uint32(newIndex[int(s.Info)])
		}
		headers = append(headers, h)
	}
	namesOff := align8(end)
	shstrtab := elf.Section64{Name: nameOf(".shstrtab"), Type: uint32(elf.SHT_STRTAB), Off: namesOff, Addralign: 1}
	shstrtab.Size = uint64(names.Len())
	headers = append(headers, shstrtab)
	shoff := align8(namesOff + shstrtab.Size)

	var tail bytes.Buffer
	tail.Write(make([]byte, namesOff-end))
	tail.Write(names.Bytes())
	tail.Write(make([]byte, shoff-(namesOff+shstrtab.Size)))
	if err := binary.Write(&tail, binary.LittleEndian, headers); err != nil {
		return err
	}
	if _, err := out.Write(tail.Bytes()); err != nil {
		return err
	}
	// e_shoff (40), e_shnum (60) and e_shstrndx (62) of the ELF header.
	var hdr [24]byte
	binary.LittleEndian.PutUint64(hdr[0:], shoff)
	if _, err := out.WriteAt(hdr[:8], 40); err != nil {
		return err
	}
	binary.LittleEndian.PutUint16(hdr[8:], uint16(len(headers)))
	binary.LittleEndian.PutUint16(hdr[10:], uint16(len(headers)-1))
	if _, err := out.WriteAt(hdr[8:12], 60); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	// The result must still read as the same ELF file.
	g, err := elf.Open(dst)
	if err != nil {
		return fmt.Errorf("stripping %s: %w", src, err)
	}
	defer g.Close()
	if len(g.Progs) != len(f.Progs) {
		return fmt.Errorf("stripping %s: segments changed", src)
	}
	return nil
}

func align8(n uint64) uint64 { return (n + 7) &^ 7 }
