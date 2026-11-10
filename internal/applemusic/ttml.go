package applemusic

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// TTMLToLRC converts Apple TTML lyrics to LRC. Each <p> becomes one line
// stamped with its begin time; word-timed spans collapse into their line.
// Untimed lyrics (itunes:timing="None" or <p> without begin) produce lines
// without tags. Input that is not XML is returned unchanged.
func TTMLToLRC(ttml string) (string, error) {
	if !strings.HasPrefix(strings.TrimSpace(ttml), "<") {
		return ttml, nil
	}

	dec := xml.NewDecoder(strings.NewReader(ttml))
	var (
		lines   []string
		untimed bool
		inBody  bool
		pDepth  int // >0 while inside a <p>
		pBegin  string
		pText   strings.Builder
	)
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("parse ttml: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch {
			case pDepth > 0:
				pDepth++
			case t.Name.Local == "tt":
				for _, a := range t.Attr {
					if a.Name.Local == "timing" && strings.EqualFold(a.Value, "None") {
						untimed = true
					}
				}
			case t.Name.Local == "body":
				inBody = true
			case inBody && t.Name.Local == "p":
				pDepth = 1
				pBegin = ""
				pText.Reset()
				for _, a := range t.Attr {
					if a.Name.Local == "begin" && a.Name.Space == "" {
						pBegin = a.Value
					}
				}
			}
		case xml.EndElement:
			switch {
			case pDepth > 1:
				pDepth--
			case pDepth == 1:
				pDepth = 0
				line := strings.TrimSpace(pText.String())
				if untimed || pBegin == "" {
					lines = append(lines, line)
					continue
				}
				tag, err := lrcTag(pBegin)
				if err != nil {
					return "", err
				}
				lines = append(lines, tag+line)
			case t.Name.Local == "body":
				inBody = false
			}
		case xml.CharData:
			if pDepth > 0 {
				pText.Write(t)
			}
		}
	}
	if len(lines) == 0 {
		return "", nil
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// lrcTag converts a TTML clock value (ss.fff, m:ss.fff or h:mm:ss.fff,
// optional trailing "s") to "[mm:ss.xx]".
func lrcTag(v string) (string, error) {
	s := strings.TrimSuffix(strings.TrimSpace(v), "s")
	fields := strings.Split(s, ":")
	if len(fields) > 3 {
		return "", fmt.Errorf("parse ttml time %q: too many fields", v)
	}
	secPart, frac, _ := strings.Cut(fields[len(fields)-1], ".")
	sec, err := strconv.Atoi(secPart)
	if err != nil {
		return "", fmt.Errorf("parse ttml time %q: %w", v, err)
	}
	if len(frac) > 2 {
		frac = frac[:2]
	}
	cs := 0
	if frac != "" {
		frac += strings.Repeat("0", 2-len(frac))
		if cs, err = strconv.Atoi(frac); err != nil {
			return "", fmt.Errorf("parse ttml time %q: %w", v, err)
		}
	}
	total := sec*100 + cs
	mult := 6000
	for i := len(fields) - 2; i >= 0; i-- {
		n, err := strconv.Atoi(fields[i])
		if err != nil {
			return "", fmt.Errorf("parse ttml time %q: %w", v, err)
		}
		total += n * mult
		mult *= 60
	}
	return fmt.Sprintf("[%02d:%02d.%02d]", total/6000, total/100%60, total%100), nil
}
