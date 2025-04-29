// TODO: This code is hacked together and needs to be refactored.
// It generates PlantUML diagrams but the structure and organization could be improved.
package plantuml

import (
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/runpod/hsm/v2/elements"
	"github.com/runpod/hsm/v2/kind"
)

func idFromQualifiedName(qualifiedName string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(strings.TrimPrefix(strings.TrimPrefix(qualifiedName, "/"), "."), "-", "_"), "/.", "/"), "/", ".")
}

func generateState(builder *strings.Builder, depth int, state elements.NamedElement, model elements.Model, allElements []elements.NamedElement, visited map[string]any) {
	if state.QualifiedName() == "/" {
		return
	}
	id := idFromQualifiedName(state.QualifiedName())
	indent := strings.Repeat(" ", depth*2)
	composite := false
	visited[state.QualifiedName()] = struct{}{}
	for _, element := range allElements {
		if _, ok := visited[element.QualifiedName()]; ok {
			continue
		}
		if element.Owner() == state.QualifiedName() {
			if kind.IsKind(element.Kind(), kind.Vertex) {
				if !composite {
					composite = true
					fmt.Fprintf(builder, "%sstate %s{\n", indent, id)
				}
				generateVertex(builder, depth+1, element, model, allElements, visited)
			}
		}
	}
	initial, ok := model.Members()[path.Join(state.QualifiedName(), ".initial")]
	if ok {
		if !composite {
			composite = true
			fmt.Fprintf(builder, "%sstate %s{\n", indent, id)
		}
		if transition, ok := model.Members()[initial.(elements.Vertex).Transitions()[0]]; ok {
			generateTransition(builder, depth+1, transition.(elements.Transition), allElements, visited)
		}
	}
	if composite {
		fmt.Fprintf(builder, "%s}\n", indent)
	} else {
		tag := ""
		if kind.IsKind(state.Kind(), kind.Choice) {
			tag = " <<choice>> "
		}
		fmt.Fprintf(builder, "%sstate %s%s\n", indent, id, tag)
	}
	if kind.IsKind(state.Kind(), kind.State) {
		state := state.(elements.State)
		for _, entry := range state.Entry() {
			fmt.Fprintf(builder, "%sstate %s: entry / %s\n", indent, id, idFromQualifiedName(path.Base(entry)))
		}
		if len(state.Activities()) > 0 {
			activities := []string{}
			for _, activity := range state.Activities() {
				activities = append(activities, idFromQualifiedName(path.Base(activity)))
			}
			fmt.Fprintf(builder, "%sstate %s: activities %s\n", indent, id, strings.Join(activities, ", "))
		}
		for _, exit := range state.Exit() {
			fmt.Fprintf(builder, "%sstate %s: exit / %s\n", indent, id, idFromQualifiedName(path.Base(exit)))
		}
	}
}

func generateVertex(builder *strings.Builder, depth int, vertex elements.NamedElement, model elements.Model, allElements []elements.NamedElement, visited map[string]any) {
	if kind.IsKind(vertex.Kind(), kind.State) {
		generateState(builder, depth, vertex, model, allElements, visited)
	}
}

func generateTransition(builder *strings.Builder, depth int, transition elements.Transition, _ []elements.NamedElement, visited map[string]any) {
	visited[transition.QualifiedName()] = struct{}{}
	source := transition.Source()
	label := ""
	if strings.HasSuffix(source, ".initial") {
		source = "[*]"
	} else {
		if len(transition.Events()) > 0 {
			names := []string{}
			for _, event := range transition.Events() {
				names = append(names, idFromQualifiedName(path.Base(event.Name)))
			}
			label = strings.Join(names, "|")
		}
	}
	if guard := transition.Guard(); guard != "" {
		label = fmt.Sprintf("%s [%s]", label, idFromQualifiedName(path.Base(guard)))
	}
	for _, effect := range transition.Effect() {
		label = fmt.Sprintf("%s / %s", label, idFromQualifiedName(path.Base(effect)))
	}
	if label != "" {
		label = fmt.Sprintf(" : %s", label)
	}
	indent := strings.Repeat(" ", depth*2)
	if transition.Kind() == kind.Internal {
		fmt.Fprintf(builder, "%sstate %s%s\n", indent, idFromQualifiedName(source), label)
	} else {
		target := transition.Target()
		fmt.Fprintf(builder, "%s%s ----> %s%s\n", indent, idFromQualifiedName(source), idFromQualifiedName(target), label)
	}

}

func generateElements(builder *strings.Builder, depth int, model elements.Model, allElements []elements.NamedElement, visited map[string]any) {
	fmt.Fprintf(builder, "@startuml %s\n", path.Base(model.Id()))
	for _, element := range allElements {
		if _, ok := visited[element.QualifiedName()]; ok {
			continue
		}
		if kind.IsKind(element.Kind(), kind.State, kind.Choice) {
			generateState(builder, depth+1, element, model, allElements, visited)
		}
	}
	if initial, ok := model.Members()[path.Join(model.QualifiedName(), ".initial")]; ok {
		if transition, ok := model.Members()[initial.(elements.Vertex).Transitions()[0]]; ok {
			generateTransition(builder, depth, transition.(elements.Transition), allElements, visited)
		}
	}
	for _, element := range allElements {
		if kind.IsKind(element.Kind(), kind.Transition) {
			transition := element.(elements.Transition)
			if !strings.HasSuffix(transition.Source(), ".initial") {
				generateTransition(builder, depth, transition, allElements, visited)
			}
		}
	}
	fmt.Fprintln(builder, "@enduml")
}

func Generate(writer io.Writer, model elements.Model) error {
	var builder strings.Builder
	elements := []elements.NamedElement{}
	for _, element := range model.Members() {
		elements = append(elements, element)
	}
	// Sort elements hierarchically like a directory structure
	sort.Slice(elements, func(i, j int) bool {
		iPath := strings.Split(elements[i].QualifiedName(), "/")
		jPath := strings.Split(elements[j].QualifiedName(), "/")

		// Compare each path segment
		minLen := len(iPath)
		if len(jPath) < minLen {
			minLen = len(jPath)
		}

		for k := 0; k < minLen; k++ {
			if iPath[k] != jPath[k] {
				return iPath[k] < jPath[k]
			}
		}

		// If all segments match up to minLen, shorter path comes first
		return len(iPath) < len(jPath)
	})

	generateElements(&builder, 0, model, elements, map[string]any{})
	_, err := writer.Write([]byte(builder.String()))
	return err
}
