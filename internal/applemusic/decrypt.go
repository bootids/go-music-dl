package applemusic

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/Eyevinn/mp4ff/mp4"
	"golang.org/x/sync/errgroup"
)

// SampleDecrypter decrypts one cbcs-protected range (a multiple of 16 bytes)
// with the CBC chain starting fresh from the track's constant IV.
// *temari.Template satisfies it.
type SampleDecrypter interface{ DecryptInto(dst, src []byte) }

// KeySource returns the decrypter for a playlist key URI.
type KeySource func(ctx context.Context, keyURI string) (SampleDecrypter, error)

const decryptWorkers = 4

type fragJob struct {
	idx  int
	frag *mp4.Fragment
	dec  SampleDecrypter
}

// DecryptStream reads an encrypted fragmented MP4 (init segment followed by
// moof/mdat fragments) from in and writes the clear fragmented MP4 to out.
// Fragment i is decrypted with the key of segs[i].
func DecryptStream(ctx context.Context, in io.Reader, out io.Writer, segs []Segment, keys KeySource) error {
	r := bufio.NewReaderSize(in, 1<<20)
	init, pos, err := readInit(r)
	if err != nil {
		return err
	}
	tracks, ini, err := prepareInit(init)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(out, 1<<20)
	if err := init.Encode(w); err != nil {
		return fmt.Errorf("encode init: %w", err)
	}

	g, gctx := errgroup.WithContext(ctx)
	jobs := make(chan fragJob, decryptWorkers)
	results := make(chan fragJob, decryptWorkers)

	// Reader: sequential box reads; keys fetched at most once per URI.
	g.Go(func() error {
		defer close(jobs)
		cache := map[string]SampleDecrypter{}
		for i := 0; ; i++ {
			if err := gctx.Err(); err != nil {
				return err
			}
			frag, next, err := readFragment(r, pos)
			if err != nil {
				return err
			}
			pos = next
			if frag == nil {
				if i < len(segs) {
					return fmt.Errorf("%w: stream ended after %d of %d segments", ErrSegmentMismatch, i, len(segs))
				}
				return nil
			}
			if i >= len(segs) {
				return fmt.Errorf("%w: fragment %d but only %d segments", ErrSegmentMismatch, i, len(segs))
			}
			uri := fragmentKeyURI(segs, i, ini, frag)
			dec, ok := cache[uri]
			if !ok {
				if dec, err = keys(gctx, uri); err != nil {
					return fmt.Errorf("key for segment %d: %w", i, err)
				}
				cache[uri] = dec
			}
			select {
			case jobs <- fragJob{idx: i, frag: frag, dec: dec}:
			case <-gctx.Done():
				return gctx.Err()
			}
		}
	})

	var wg sync.WaitGroup
	for n := 0; n < decryptWorkers; n++ {
		wg.Add(1)
		g.Go(func() error {
			defer wg.Done()
			for {
				var job fragJob
				var ok bool
				select {
				case job, ok = <-jobs:
					if !ok {
						return nil
					}
				case <-gctx.Done():
					return gctx.Err()
				}
				if err := decryptFragment(job.frag, tracks, job.dec); err != nil {
					return fmt.Errorf("fragment %d: %w", job.idx, err)
				}
				select {
				case results <- job:
				case <-gctx.Done():
					return gctx.Err()
				}
			}
		})
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	// Writer: emit fragments in index order.
	g.Go(func() error {
		pending := map[int]*mp4.Fragment{}
		next := 0
		for {
			select {
			case res, ok := <-results:
				if !ok {
					return nil
				}
				pending[res.idx] = res.frag
				for f, ok := pending[next]; ok; f, ok = pending[next] {
					if err := f.Encode(w); err != nil {
						return fmt.Errorf("encode fragment %d: %w", next, err)
					}
					delete(pending, next)
					next++
				}
			case <-gctx.Done():
				return gctx.Err()
			}
		}
	})

	if err := g.Wait(); err != nil {
		return err
	}
	return w.Flush()
}

// readInit reads ftyp and moov from the start of the stream.
func readInit(r io.Reader) (*mp4.InitSegment, uint64, error) {
	init := mp4.NewMP4Init()
	var pos uint64
	for init.Moov == nil {
		box, err := mp4.DecodeBox(pos, r)
		if err != nil {
			return nil, pos, fmt.Errorf("read init segment: %w", err)
		}
		pos += box.Size()
		switch box.Type() {
		case "ftyp", "moov":
			init.AddChild(box)
		default:
			return nil, pos, fmt.Errorf("unexpected %s box before moov", box.Type())
		}
	}
	return init, pos, nil
}

// initInfo records what prepareInit dropped from the original init.
type initInfo struct {
	dualEntry   bool   // stsd had two entries (prefetch key, song key)
	defaultDesc uint32 // trex default_sample_description_index before normalizing
}

// prepareInit keeps one sample entry, converts the init to clear and returns
// the per-track decryption info.
func prepareInit(init *mp4.InitSegment) (map[uint32]mp4.DecryptTrackInfo, initInfo, error) {
	var ini initInfo
	if len(init.Moov.Traks) != 1 {
		return nil, ini, fmt.Errorf("expected 1 track, got %d", len(init.Moov.Traks))
	}
	stbl := init.Moov.Trak.Mdia.Minf.Stbl
	stsd := stbl.Stsd
	switch len(stsd.Children) {
	case 1:
	case 2:
		if stsd.Children[0].Type() != stsd.Children[1].Type() {
			return nil, ini, fmt.Errorf("stsd entries differ: %s, %s", stsd.Children[0].Type(), stsd.Children[1].Type())
		}
		stsd.Children = stsd.Children[:1]
		stsd.SampleCount = 1
		ini.dualEntry = true
	default:
		return nil, ini, fmt.Errorf("expected 1 or 2 stsd entries, got %d", len(stsd.Children))
	}
	// Only entry 1 remains, so every sample must point at it.
	ini.defaultDesc = 1
	if mvex := init.Moov.Mvex; mvex != nil {
		for i, trex := range mvex.Trexs {
			if i == 0 {
				ini.defaultDesc = trex.DefaultSampleDescriptionIndex
			}
			trex.DefaultSampleDescriptionIndex = 1
		}
	}

	di, err := mp4.DecryptInit(init)
	if err != nil {
		return nil, ini, fmt.Errorf("decrypt init: %w", err)
	}
	tracks := make(map[uint32]mp4.DecryptTrackInfo, len(di.TrackInfos))
	for _, ti := range di.TrackInfos {
		tracks[ti.TrackID] = ti
	}

	for _, trak := range init.Moov.Traks {
		st := trak.Mdia.Minf.Stbl
		kept := st.Children[:0]
		for _, c := range st.Children {
			switch b := c.(type) {
			case *mp4.SbgpBox:
				if isSampleEncryptionGroup(b.GroupingType) {
					continue
				}
			case *mp4.SgpdBox:
				if isSampleEncryptionGroup(b.GroupingType) {
					continue
				}
			}
			kept = append(kept, c)
		}
		st.Children = kept
	}
	return tracks, ini, nil
}

// fragmentKeyURI returns the key URI for fragment i. With Apple's two-entry
// stsd the fragment's sample description index names the key that encrypted
// it: entry 1 is the prefetch key and entry 2 the song key. Otherwise, or for
// any other index, the playlist position decides.
func fragmentKeyURI(segs []Segment, i int, ini initInfo, frag *mp4.Fragment) string {
	uri := segs[i].KeyURI
	if !ini.dualEntry || frag.Moof == nil || frag.Moof.Traf == nil || frag.Moof.Traf.Tfhd == nil {
		return uri
	}
	desc := ini.defaultDesc
	if tfhd := frag.Moof.Traf.Tfhd; tfhd.HasSampleDescriptionIndex() {
		desc = tfhd.SampleDescriptionIndex
	}
	switch desc {
	case 1:
		return PrefetchKeyURI
	case 2:
		if uri != "" && uri != PrefetchKeyURI {
			return uri
		}
		for _, s := range segs {
			if s.KeyURI != "" && s.KeyURI != PrefetchKeyURI {
				return s.KeyURI
			}
		}
	}
	return uri
}

func isSampleEncryptionGroup(t string) bool { return t == "seig" || t == "seam" }

// readFragment reads the next moof..mdat fragment. It returns a nil fragment
// at a clean end of stream.
func readFragment(r io.Reader, pos uint64) (*mp4.Fragment, uint64, error) {
	frag := mp4.NewFragment()
	for {
		box, err := mp4.DecodeBox(pos, r)
		if errors.Is(err, io.EOF) && frag.Moof == nil {
			return nil, pos, nil
		}
		if err != nil {
			return nil, pos, fmt.Errorf("read fragment at offset %d: %w", pos, err)
		}
		pos += box.Size()
		switch box.Type() {
		case "styp", "emsg", "prft":
			frag.AddChild(box)
		case "moof":
			if frag.Moof != nil {
				return nil, pos, fmt.Errorf("moof without mdat at offset %d", pos)
			}
			frag.AddChild(box)
		case "mdat":
			if frag.Moof == nil {
				return nil, pos, fmt.Errorf("mdat without moof at offset %d", pos)
			}
			frag.AddChild(box)
			return frag, pos, nil
		}
		// Other top-level boxes (sidx, free, mfra, ...) are dropped.
	}
}

// decryptFragment decrypts all protected samples of frag in place and strips
// the encryption boxes.
func decryptFragment(frag *mp4.Fragment, tracks map[uint32]mp4.DecryptTrackInfo, dec SampleDecrypter) error {
	moof := frag.Moof
	var removed uint64
	for _, traf := range moof.Trafs {
		ti, ok := tracks[traf.Tfhd.TrackID]
		if !ok {
			return fmt.Errorf("no track info for track %d", traf.Tfhd.TrackID)
		}
		if ti.Sinf == nil {
			continue
		}
		if ti.Sinf.Schm == nil || ti.Sinf.Schm.SchemeType != "cbcs" {
			scheme := ""
			if ti.Sinf.Schm != nil {
				scheme = ti.Sinf.Schm.SchemeType
			}
			return fmt.Errorf("%w: %q", ErrUnsupportedScheme, scheme)
		}
		if ti.Sinf.Schi == nil || ti.Sinf.Schi.Tenc == nil {
			return errors.New("missing tenc box")
		}
		tenc := ti.Sinf.Schi.Tenc
		if tenc.DefaultSkipByteBlock != 0 {
			return fmt.Errorf("%w: crypt %d skip %d", ErrUnsupportedPattern, tenc.DefaultCryptByteBlock, tenc.DefaultSkipByteBlock)
		}

		var senc *mp4.SencBox
		hasSenc, parsed := traf.ContainsSencBox()
		switch {
		case hasSenc:
			if traf.Senc != nil {
				senc = traf.Senc
			} else {
				senc = traf.UUIDSenc.Senc
			}
			if !parsed {
				if err := senc.ParseReadBox(tenc.DefaultPerSampleIVSize, traf.Saiz); err != nil {
					return fmt.Errorf("parse senc: %w", err)
				}
			}
		case tenc.DefaultPerSampleIVSize == 0 && len(tenc.DefaultConstantIV) > 0:
			// Full-sample encryption with a constant IV may omit senc (CMAF 8.2.2.1).
		default:
			return errors.New("no senc box in traf")
		}

		samples, err := frag.GetFullSamples(ti.Trex)
		if err != nil {
			return err
		}
		if senc != nil && len(senc.SubSamples) != 0 && len(senc.SubSamples) < len(samples) {
			return fmt.Errorf("senc has %d subsample entries for %d samples", len(senc.SubSamples), len(samples))
		}
		for i := range samples {
			var subs []mp4.SubSamplePattern
			if senc != nil && len(senc.SubSamples) != 0 {
				subs = senc.SubSamples[i]
			}
			if err := decryptSample(samples[i].Data, subs, dec); err != nil {
				return fmt.Errorf("sample %d: %w", i, err)
			}
		}
		removed += traf.RemoveEncryptionBoxes()
	}
	_, psshBytes := moof.RemovePsshs()
	removed += psshBytes
	for _, traf := range moof.Trafs {
		// prepareInit kept only stsd entry 1.
		if traf.Tfhd.HasSampleDescriptionIndex() {
			traf.Tfhd.SampleDescriptionIndex = 1
		}
		for _, trun := range traf.Truns {
			trun.DataOffset -= int32(removed)
		}
	}
	return nil
}

// decryptSample decrypts the 16-byte-aligned prefix of the whole sample, or of
// each subsample's protected range, each with its own DecryptInto call.
func decryptSample(sample []byte, subs []mp4.SubSamplePattern, dec SampleDecrypter) error {
	if len(subs) == 0 {
		decryptAligned(sample, dec)
		return nil
	}
	var pos uint64
	for _, ss := range subs {
		pos += uint64(ss.BytesOfClearData)
		end := pos + uint64(ss.BytesOfProtectedData)
		if end > uint64(len(sample)) {
			return fmt.Errorf("subsample range %d..%d exceeds sample size %d", pos, end, len(sample))
		}
		decryptAligned(sample[pos:end], dec)
		pos = end
	}
	return nil
}

func decryptAligned(b []byte, dec SampleDecrypter) {
	n := len(b) &^ 0xf
	if n > 0 {
		dec.DecryptInto(b[:n], b[:n])
	}
}
