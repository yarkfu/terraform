package terraform

import (
	"log"

	"github.com/hashicorp/terraform/configs"
	"github.com/hashicorp/terraform/dag"
)

// ModuleExpansionTransformer is a GraphTransformer that adds graph nodes
// representing the possible expansion of each module call in the configuration,
// and ensures that any nodes representing objects declared within a module
// are dependent on the expansion node so that they will be visited only
// after the module expansion has been decided.
//
// This transform must be applied only after all nodes representing objects
// that can be contained within modules have already been added.
type ModuleExpansionTransformer struct {
	Config   *configs.Config
	Concrete ConcreteModuleNodeFunc
}

func (t *ModuleExpansionTransformer) Transform(g *Graph) error {
	// The root module is always a singleton and so does not need expansion
	// processing, but any descendent modules do. We'll process them
	// recursively using t.transform.
	for _, cfg := range t.Config.Children {
		err := t.transform(g, cfg, nil)
		if err != nil {
			return err
		}
	}
	return nil
}

func (t *ModuleExpansionTransformer) transform(g *Graph, c *configs.Config, parentNode dag.Vertex) error {
	_, call := c.Path.Call()
	modCall := c.Parent.Module.ModuleCalls[call.Name]

	n := &nodeExpandModule{
		Addr:       c.Path,
		Config:     c.Module,
		ModuleCall: modCall,
	}
	var v dag.Vertex = n
	if t.Concrete != nil {
		v = t.Concrete(n)
	}

	g.Add(v)
	log.Printf("[TRACE] ModuleExpansionTransformer: Added %s as %T", c.Path, v)

	if parentNode != nil {
		log.Printf("[TRACE] ModuleExpansionTransformer: %s must wait for expansion of %s", dag.VertexName(v), dag.VertexName(parentNode))
		g.Connect(dag.BasicEdge(v, parentNode))
	}

	// Add the root module node to provide a single exit point for the expanded
	// module.
	moduleRoot := &nodeCloseModule{
		Addr: c.Path,
	}
	g.Add(moduleRoot)
	g.Connect(dag.BasicEdge(moduleRoot, v))

	// Connect any node that reports this module as its Path to ensure that
	// the module expansion will be handled before that node.
	for _, childV := range g.Vertices() {
		pather, ok := childV.(GraphNodeModulePath)
		if !ok {
			continue
		}
		if pather.ModulePath().Equal(c.Path) {
			log.Printf("[TRACE] ModuleExpansionTransformer: %s must wait for expansion of %s", dag.VertexName(childV), c.Path)
			g.Connect(dag.BasicEdge(childV, v))

			// The module root also depends on each child instance, since
			// during apply the module expansion will complete before the
			// individual instances are applied.
			g.Connect(dag.BasicEdge(moduleRoot, childV))
		}
	}

	// Also visit child modules, recursively.
	for _, cc := range c.Children {
		return t.transform(g, cc, v)
	}

	return nil
}
