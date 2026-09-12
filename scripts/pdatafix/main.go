// scripts/pdatafix/main.go
//
// Makes a Go c-archive linkable by MSVC.
//
// MSVC's linker requires the .pdata section's RUNTIME_FUNCTION array to be
// ordered by function start address, and rejects the whole object when it is
// not:
//
//	go.o : fatal error LNK1223: invalid or corrupt file:
//	       file contains invalid .pdata contributions
//
// Go's linker does not guarantee that order. It is usually sorted by accident
// and occasionally is not -- a 5,000-function archive with 35 inversions in it
// links fine with mingw's ld and not at all with link.exe -- which is why this
// reads as an intermittent, version-dependent failure rather than a rule.
//
// This sorts the array in place and leaves everything else alone. The record
// count and the relocation count do not change, so the archive is patched
// byte-for-byte at the same offsets: no member is resized, no header is
// rewritten, and running it twice is a no-op.
//
// WHAT MAKES THIS MORE THAN A SORT. Every BeginAddress field in the object is
// zero. The real address lives in a relocation over that field, so the sort
// key has to be resolved through the relocation to the target symbol's section
// offset -- and the three relocations belonging to each record (BeginAddress,
// EndAddress, UnwindInfoAddress) have to travel with the record they describe.
// Moving the 12-byte records without repointing their relocations produces an
// object that is sorted, still passes this tool's own check, and describes the
// wrong unwind data for every function. That is worse than the error it
// replaces.
//
// Written in Go rather than Python so it adds no build dependency: the Go
// toolchain is already required to produce the archive it operates on.
//
//	go run ./scripts/pdatafix build/omegamaps.lib
//	go run ./scripts/pdatafix -check build/omegamaps.lib
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"sort"
)

const (
	coffHeaderSize  = 20
	sectionSize     = 40
	symbolSize      = 18
	relocSize       = 10
	runtimeFuncSize = 12 // BeginAddress, EndAddress, UnwindInfoAddress

	machineAMD64 = 0x8664
	nrelocOvfl   = 0x01000000 // IMAGE_SCN_LNK_NRELOC_OVFL
)

func main() {
	check := flag.Bool("check", false, "report the ordering and change nothing")
	verify := flag.Bool("verify", false, "check the table is self-consistent and change nothing")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: pdatafix [-check] <archive.lib | go.o>")
		os.Exit(2)
	}
	path := flag.Arg(0)

	blob, err := os.ReadFile(path)
	if err != nil {
		fail(err)
	}

	objOff, objLen, err := findObject(blob)
	if err != nil {
		fail(err)
	}
	obj := blob[objOff : objOff+objLen]

	if *verify {
		if err := verifyPdata(obj); err != nil {
			fail(err)
		}
		return
	}

	n, inversions, err := sortPdata(obj, *check)
	if err != nil {
		fail(err)
	}

	switch {
	case inversions == 0:
		fmt.Printf("pdatafix: %d RUNTIME_FUNCTIONs already ordered, nothing to do\n", n)
	case *check:
		fmt.Printf("pdatafix: %d RUNTIME_FUNCTIONs, %d out of order (run without -check to fix)\n", n, inversions)
		os.Exit(1)
	default:
		if err := os.WriteFile(path, blob, 0o644); err != nil {
			fail(err)
		}
		fmt.Printf("pdatafix: reordered %d RUNTIME_FUNCTIONs (%d inversions) in %s\n",
			n, inversions, path)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "pdatafix:", err)
	os.Exit(1)
}

// findObject locates the go.o member inside a COFF archive, or accepts a bare
// object file. Returns its offset and length within blob.
func findObject(blob []byte) (int, int, error) {
	if !bytes.HasPrefix(blob, []byte("!<arch>\n")) {
		return 0, len(blob), nil
	}
	var longNames []byte
	pos := 8
	for pos+60 <= len(blob) {
		hdr := blob[pos : pos+60]
		name := string(bytes.TrimRight(hdr[0:16], " "))
		var size int
		if _, err := fmt.Sscanf(string(bytes.TrimSpace(hdr[48:58])), "%d", &size); err != nil {
			return 0, 0, fmt.Errorf("malformed archive member header at %d", pos)
		}
		body := pos + 60
		if body+size > len(blob) {
			return 0, 0, fmt.Errorf("archive member at %d runs past end of file", pos)
		}
		switch {
		case name == "//":
			longNames = blob[body : body+size]
		case len(name) > 1 && name[0] == '/' && name[1] >= '0' && name[1] <= '9':
			var off int
			fmt.Sscanf(name[1:], "%d", &off)
			if off < len(longNames) {
				if end := bytes.IndexByte(longNames[off:], '/'); end >= 0 {
					name = string(longNames[off : off+end])
				}
			}
		}
		if trimmed := bytes.TrimRight([]byte(name), "/"); string(trimmed) == "go.o" {
			return body, size, nil
		}
		pos = body + size + (size & 1)
	}
	return 0, 0, fmt.Errorf("no go.o member in this archive")
}

type record struct {
	key    uint32   // resolved BeginAddress: target symbol's section offset
	data   [12]byte // the RUNTIME_FUNCTION itself
	relocs [3][]byte
}

// sortPdata reorders obj's .pdata records in place. When dry is true nothing is
// written and only the count of inversions is reported.
func sortPdata(obj []byte, dry bool) (count, inversions int, err error) {
	if len(obj) < coffHeaderSize {
		return 0, 0, fmt.Errorf("not a COFF object (too short)")
	}
	if machine := binary.LittleEndian.Uint16(obj[0:]); machine != machineAMD64 {
		return 0, 0, fmt.Errorf("not an x86-64 object (machine 0x%04x)", machine)
	}
	nsec := int(binary.LittleEndian.Uint16(obj[2:]))
	symPtr := int(binary.LittleEndian.Uint32(obj[8:]))
	nsym := int(binary.LittleEndian.Uint32(obj[12:]))
	optSize := int(binary.LittleEndian.Uint16(obj[16:]))

	// Locate .pdata.
	var rawPtr, rawSize, relPtr, nrel int
	base := coffHeaderSize + optSize
	for i := 0; i < nsec; i++ {
		h := obj[base+i*sectionSize:]
		if string(bytes.TrimRight(h[0:8], "\x00")) != ".pdata" {
			continue
		}
		rawSize = int(binary.LittleEndian.Uint32(h[16:]))
		rawPtr = int(binary.LittleEndian.Uint32(h[20:]))
		relPtr = int(binary.LittleEndian.Uint32(h[24:]))
		nrel = int(binary.LittleEndian.Uint16(h[32:]))
		flags := binary.LittleEndian.Uint32(h[36:])
		// With more than 65535 relocations the count does not fit the header
		// field; it lives in the first relocation record instead, and the real
		// table starts one record later.
		if flags&nrelocOvfl != 0 {
			nrel = int(binary.LittleEndian.Uint32(obj[relPtr:])) - 1
			relPtr += relocSize
		}
		break
	}
	if rawSize == 0 {
		return 0, 0, fmt.Errorf("no .pdata section, or it is empty")
	}
	count = rawSize / runtimeFuncSize
	if want := count * 3; nrel != want {
		return 0, 0, fmt.Errorf(
			".pdata has %d relocations for %d records; expected %d", nrel, count, want)
	}

	// Symbol values, walking aux entries so indices stay aligned.
	symValue := make([]uint32, nsym)
	for i := 0; i < nsym; {
		e := obj[symPtr+i*symbolSize:]
		symValue[i] = binary.LittleEndian.Uint32(e[8:])
		naux := int(e[17])
		for j := 1; j <= naux && i+j < nsym; j++ {
			symValue[i+j] = 0
		}
		i += 1 + naux
	}

	// Index relocations by the offset they patch, then pair each record with
	// its three. A record whose relocations are missing means an object shaped
	// differently from what this understands, and guessing would be worse than
	// stopping.
	byOffset := make(map[uint32][]byte, nrel)
	for i := 0; i < nrel; i++ {
		r := obj[relPtr+i*relocSize : relPtr+(i+1)*relocSize]
		byOffset[binary.LittleEndian.Uint32(r[0:])] = r
	}

	recs := make([]record, count)
	for i := 0; i < count; i++ {
		off := uint32(i * runtimeFuncSize)
		var rec record
		copy(rec.data[:], obj[rawPtr+i*runtimeFuncSize:])
		for f := 0; f < 3; f++ {
			r, ok := byOffset[off+uint32(f*4)]
			if !ok {
				return 0, 0, fmt.Errorf("record %d has no relocation at +%d", i, f*4)
			}
			rec.relocs[f] = append([]byte(nil), r...)
		}
		symIdx := int(binary.LittleEndian.Uint32(rec.relocs[0][4:]))
		if symIdx >= nsym {
			return 0, 0, fmt.Errorf("record %d names symbol %d, out of range", i, symIdx)
		}
		// The stored field is an addend on top of the symbol's value; it is
		// zero in practice but adding it costs nothing and is correct.
		rec.key = symValue[symIdx] + binary.LittleEndian.Uint32(rec.data[0:])
		recs[i] = rec
	}

	for i := 0; i+1 < count; i++ {
		if recs[i].key > recs[i+1].key {
			inversions++
		}
	}
	if inversions == 0 || dry {
		return count, inversions, nil
	}

	// Stable, so records sharing a start address keep their relative order.
	sort.SliceStable(recs, func(a, b int) bool { return recs[a].key < recs[b].key })

	// Write records back, repointing each one's relocations at its new home.
	relocs := make([][]byte, 0, nrel)
	for i, rec := range recs {
		copy(obj[rawPtr+i*runtimeFuncSize:], rec.data[:])
		for f := 0; f < 3; f++ {
			r := rec.relocs[f]
			binary.LittleEndian.PutUint32(r[0:], uint32(i*runtimeFuncSize+f*4))
			relocs = append(relocs, r)
		}
	}
	// Emit the table in offset order. COFF does not require it, but every
	// consumer assumes it and a scrambled table is a needless difference from
	// what the toolchain would have produced.
	sort.Slice(relocs, func(a, b int) bool {
		return binary.LittleEndian.Uint32(relocs[a][0:]) <
			binary.LittleEndian.Uint32(relocs[b][0:])
	})
	for i, r := range relocs {
		copy(obj[relPtr+i*relocSize:], r)
	}
	return count, inversions, nil
}

// verifyPdata checks the table against the invariants Go's own output
// satisfies, so a reordering can be checked rather than trusted.
//
// Sortedness is the weak half of this and would pass even if the records had
// been shuffled free of their relocations. The load-bearing check is that each
// record's BeginAddress and EndAddress relocations still name the SAME symbol:
// Go emits End as that symbol plus the function's length, so a record paired
// with someone else's relocations shows up here as a function whose end is not
// after its start, or as two relocations naming different functions. Either is
// the failure mode that would leave a binary linking cleanly and unwinding
// into nonsense.
func verifyPdata(obj []byte) error {
	nsym := int(binary.LittleEndian.Uint32(obj[12:]))
	symPtr := int(binary.LittleEndian.Uint32(obj[8:]))
	nsec := int(binary.LittleEndian.Uint16(obj[2:]))
	optSize := int(binary.LittleEndian.Uint16(obj[16:]))

	var rawPtr, rawSize, relPtr, nrel int
	base := coffHeaderSize + optSize
	for i := 0; i < nsec; i++ {
		h := obj[base+i*sectionSize:]
		if string(bytes.TrimRight(h[0:8], "\x00")) != ".pdata" {
			continue
		}
		rawSize = int(binary.LittleEndian.Uint32(h[16:]))
		rawPtr = int(binary.LittleEndian.Uint32(h[20:]))
		relPtr = int(binary.LittleEndian.Uint32(h[24:]))
		nrel = int(binary.LittleEndian.Uint16(h[32:]))
		if binary.LittleEndian.Uint32(h[36:])&nrelocOvfl != 0 {
			nrel = int(binary.LittleEndian.Uint32(obj[relPtr:])) - 1
			relPtr += relocSize
		}
		break
	}
	if rawSize == 0 {
		return fmt.Errorf("no .pdata section")
	}
	count := rawSize / runtimeFuncSize

	symValue := make([]uint32, nsym)
	symSection := make([]int16, nsym)
	for i := 0; i < nsym; {
		e := obj[symPtr+i*symbolSize:]
		symValue[i] = binary.LittleEndian.Uint32(e[8:])
		symSection[i] = int16(binary.LittleEndian.Uint16(e[12:]))
		naux := int(e[17])
		i += 1 + naux
	}

	type entry struct{ symIdx [3]int }
	byOffset := make(map[uint32]int, nrel)
	for i := 0; i < nrel; i++ {
		r := obj[relPtr+i*relocSize:]
		byOffset[binary.LittleEndian.Uint32(r[0:])] = int(binary.LittleEndian.Uint32(r[4:]))
	}

	var problems []string
	note := func(format string, a ...interface{}) {
		if len(problems) < 12 {
			problems = append(problems, fmt.Sprintf(format, a...))
		}
	}

	prevEnd := uint32(0)
	prevBegin := uint32(0)
	for i := 0; i < count; i++ {
		var e entry
		for f := 0; f < 3; f++ {
			idx, ok := byOffset[uint32(i*runtimeFuncSize+f*4)]
			if !ok {
				note("record %d: no relocation at +%d", i, f*4)
				continue
			}
			e.symIdx[f] = idx
		}
		if e.symIdx[0] != e.symIdx[1] {
			note("record %d: Begin names symbol %d but End names %d "+
				"(record separated from its relocations)", i, e.symIdx[0], e.symIdx[1])
			continue
		}
		if symSection[e.symIdx[2]] == symSection[e.symIdx[0]] {
			note("record %d: UnwindInfo points into the code section, not .xdata", i)
		}
		begin := symValue[e.symIdx[0]] + binary.LittleEndian.Uint32(obj[rawPtr+i*runtimeFuncSize:])
		end := symValue[e.symIdx[1]] + binary.LittleEndian.Uint32(obj[rawPtr+i*runtimeFuncSize+4:])
		if end <= begin {
			note("record %d: end 0x%x is not after start 0x%x", i, end, begin)
		}
		if i > 0 {
			if begin < prevBegin {
				note("record %d: start 0x%x is before the previous start 0x%x", i, begin, prevBegin)
			} else if begin < prevEnd {
				note("record %d: starts at 0x%x, inside the previous function which ends at 0x%x",
					i, begin, prevEnd)
			}
		}
		prevBegin, prevEnd = begin, end
	}

	if len(problems) == 0 {
		fmt.Printf("pdatafix: %d RUNTIME_FUNCTIONs verified -- ordered, non-overlapping, "+
			"each paired with its own relocations\n", count)
		return nil
	}
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, "  "+p)
	}
	return fmt.Errorf("%d record(s) failed verification", len(problems))
}
