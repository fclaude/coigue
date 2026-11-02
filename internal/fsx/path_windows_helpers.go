package fsx

import (
	"path/filepath"
	"strings"
)

func windowsSep(c byte) bool { return c == '/' || c == '\\' }

func windowsImplicitRoot(volume string) bool {
	return strings.HasPrefix(volume, `\\`) && (!strings.HasPrefix(volume, `\\?\`) || strings.HasPrefix(strings.ToUpper(volume), `\\?\UNC\`))
}

// windowsComponentEnds keeps offsets in the original spelling for Parent.
func windowsComponentEnds(p string) (ends []int, rooted bool) {
	volume := filepath.VolumeName(p)
	i := len(volume)
	if i > 0 {
		ends = append(ends, i)
	}
	if i < len(p) && windowsSep(p[i]) {
		rooted = true
		i++
		ends = append(ends, i)
		for i < len(p) && windowsSep(p[i]) {
			i++
		}
	}
	if !rooted && windowsImplicitRoot(volume) {
		rooted = true
		ends = append(ends, i)
	}
	first := true
	for i < len(p) {
		j := i
		for j < len(p) && !windowsSep(p[j]) {
			j++
		}
		if p[i:j] != "." || (first && !rooted && len(volume) == 0) {
			ends = append(ends, j)
		}
		first = false
		i = j
		for i < len(p) && windowsSep(p[i]) {
			i++
		}
	}
	return
}

func windowsComponents(p string) []string {
	volume := filepath.VolumeName(p)
	i := len(volume)
	var out []string
	if volume != "" {
		component := volume
		if len(volume) == 2 && volume[1] == ':' {
			component = strings.ToUpper(volume[:1]) + volume[1:]
		}
		out = append(out, component)
	}
	rooted := i < len(p) && windowsSep(p[i])
	if rooted {
		out = append(out, `\`)
		for i < len(p) && windowsSep(p[i]) {
			i++
		}
	}
	if !rooted && windowsImplicitRoot(volume) {
		rooted = true
		out = append(out, `\`)
	}
	first := true
	for i < len(p) {
		j := i
		for j < len(p) && !windowsSep(p[j]) {
			j++
		}
		if part := p[i:j]; part != "." || (first && !rooted && volume == "") {
			out = append(out, part)
		}
		first = false
		i = j
		for i < len(p) && windowsSep(p[i]) {
			i++
		}
	}
	return out
}

func windowsJoin(base, part string) string {
	volume := filepath.VolumeName(part)
	if volume != "" {
		return part
	}
	if len(part) > 0 && windowsSep(part[0]) {
		return filepath.VolumeName(base) + part
	}
	if base == "" {
		return part
	}
	if windowsSep(base[len(base)-1]) || strings.HasSuffix(base, ":") {
		return base + part
	}
	return base + `\` + part
}
