package refine

import (
	"fmt"
	"strings"
)

// ClassVerifier is the Go counterpart of the Groovy ModelClassVerifier: it fixes, for every
// model, which output index means which canonical instrument class, so that the manager never
// confuses class indices (ride/crash, closed/open/pedal, floor/mid/high, 5-class arbiter).
//
// Priority (same as the original): default canonical order < "names" list from models_meta.json
// < "class_map" ({"0": "ride", ...}). Besides the original it also validates the result:
// the number of output classes of the ONNX file must match, and every class the manager needs
// must appear exactly once.

var instrumentDescription = map[string]string{
	"ride":       "Ride cymbal",
	"crash":      "Crash cymbal",
	"closed_hat": "Closed hi-hat",
	"open_hat":   "Open hi-hat",
	"pedal_hat":  "Pedal (foot) hi-hat",
	"floor_tom":  "Floor / low tom",
	"mid_tom":    "Mid tom",
	"high_tom":   "High tom",
}

type verifiedModel struct {
	key      string
	mapping  []string // output index -> canonical name
	source   string   // where the mapping came from
	warnings []string
	errors   []string
}

// ClassVerifier collects the models registered during one run and prints the report.
type ClassVerifier struct {
	models []*verifiedModel
}

// Register builds and validates the mapping of one model. nOut is the number of classes of the
// ONNX output (0 if unknown). It returns the canonical names by output index and whether the
// model may be used.
func (v *ClassVerifier) Register(spec cnnSpec, nOut int, mi metaItem) ([]string, bool) {
	vm := &verifiedModel{key: spec.key, source: "default canonical order"}
	n := nOut
	if n <= 0 {
		n = spec.classes
		vm.warnings = append(vm.warnings, "output size unknown - assuming "+fmt.Sprint(n)+" classes")
	}
	if n != spec.classes {
		vm.errors = append(vm.errors, fmt.Sprintf("the ONNX output has %d classes, the pipeline expects %d - model disabled", n, spec.classes))
	}

	mapping := make([]string, n)
	for i := range mapping {
		if i < len(spec.order) {
			mapping[i] = spec.order[i]
		} else {
			mapping[i] = fmt.Sprintf("class_%d", i)
		}
	}
	if len(mi.Names) > 0 {
		vm.source = "models_meta.json names"
		for i, name := range mi.Names {
			if i < n {
				if c := canonicalize(name); c != "" {
					mapping[i] = c
				}
			}
		}
		if len(mi.Names) != n {
			vm.warnings = append(vm.warnings, fmt.Sprintf("meta lists %d names for %d output classes", len(mi.Names), n))
		}
	} else {
		vm.warnings = append(vm.warnings, "no \"names\" in models_meta.json - the default class order is ASSUMED")
	}
	if len(mi.ClassMap) > 0 {
		vm.source = "models_meta.json class_map"
		for k, name := range mi.ClassMap {
			var idx int
			if _, err := fmt.Sscanf(k, "%d", &idx); err == nil && idx >= 0 && idx < n {
				mapping[idx] = canonicalize(name)
			}
		}
	}

	// every class that the manager reads must be present exactly once
	seen := map[string]int{}
	for _, c := range mapping {
		seen[c]++
	}
	for _, need := range spec.order {
		switch seen[need] {
		case 0:
			vm.errors = append(vm.errors, "class '"+need+"' is missing in the mapping")
		case 1:
		default:
			vm.errors = append(vm.errors, "class '"+need+"' appears twice in the mapping")
		}
	}
	vm.mapping = mapping
	v.models = append(v.models, vm)
	return mapping, len(vm.errors) == 0
}

// Report prints the verification table (like printVerificationReport in the original).
func (v *ClassVerifier) Report() {
	fmt.Println("   [ModelClassVerifier] output index -> class mapping of every model:")
	for _, vm := range v.models {
		status := "OK"
		if len(vm.errors) > 0 {
			status = "ERROR"
		}
		fmt.Printf("     %-11s [%s] source: %s\n", vm.key, status, vm.source)
		for i, c := range vm.mapping {
			desc := instrumentDescription[c]
			if desc == "" {
				desc = "?"
			}
			fmt.Printf("        [%d] %-11s %s\n", i, c, desc)
		}
		for _, w := range vm.warnings {
			fmt.Printf("        [warn] %s\n", w)
		}
		for _, e := range vm.errors {
			fmt.Printf("        [ERROR] %s\n", strings.TrimSpace(e))
		}
	}
}
