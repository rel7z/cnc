package cnc

import (
	"fmt"
	"time"
)

// JobPipeline defines a sequence of tools to run automatically
type JobPipeline struct {
	ID          string
	Name        string
	Description string
	Tools       []PipelineStage
}

// PipelineStage represents one tool in the pipeline
type PipelineStage struct {
	ToolID       string
	SkipIfNoHits bool // Skip this stage if previous stage found no results
	Options      map[string]interface{}
}

var PipelinesRegistry = map[string]JobPipeline{
	"wordpress-full-discovery": {
		ID:          "wordpress-full-discovery",
		Name:        "Full WordPress Discovery & Enumeration",
		Description: "Aggressive discovery → CMS detection → Plugin enumeration (auto-pipeline)",
		Tools: []PipelineStage{
			{
				ToolID:       "wp-discovery",
				SkipIfNoHits: false,
				Options: map[string]interface{}{
					"threads":     2000,
					"timeout":     5,
					"ports":       "80,443,8080,8888,8081",
					"ct":          true,
					"dns-rev":     true,
					"cidr":        false,
					"level":       "max",
				},
			},
			{
				ToolID:       "cms-scan",
				SkipIfNoHits: true,
				Options: map[string]interface{}{
					"auto_concurrency": true,
					"threads":          256,
					"timeout":          10,
				},
			},
			{
				ToolID:       "enum",
				SkipIfNoHits: true,
				Options: map[string]interface{}{
					"auto_concurrency": true,
					"threads":          256,
					"timeout":          10,
				},
			},
			{
				ToolID:       "wp-plugins-detector",
				SkipIfNoHits: true,
				Options: map[string]interface{}{
					"threads": 50,
					"plugins": "./tools/plugins.txt",
				},
			},
		},
	},

	"wordpress-discovery-only": {
		ID:          "wordpress-discovery-only",
		Name:        "WordPress Discovery Only",
		Description: "Aggressive discovery of fresh WordPress sites (no scanning)",
		Tools: []PipelineStage{
			{
				ToolID:       "wp-discovery",
				SkipIfNoHits: false,
				Options: map[string]interface{}{
					"threads":     3000,
					"timeout":     5,
					"ports":       "80,443,8080",
					"ct":          true,
					"dns-rev":     true,
					"cidr":        false,
					"level":       "max",
				},
			},
		},
	},

	"wordpress-aggressive-scan": {
		ID:          "wordpress-aggressive-scan",
		Name:        "Aggressive WordPress Scanning",
		Description: "CMS detection + full plugin + version enumeration (high resource usage)",
		Tools: []PipelineStage{
			{
				ToolID:       "cms-scan",
				SkipIfNoHits: false,
				Options: map[string]interface{}{
					"auto_concurrency": true,
					"threads":          256,
					"timeout":          10,
				},
			},
			{
				ToolID:       "enum",
				SkipIfNoHits: true,
				Options: map[string]interface{}{
					"auto_concurrency": true,
					"threads":          256,
					"timeout":          10,
				},
			},
			{
				ToolID:       "wp-plugins-detector",
				SkipIfNoHits: true,
				Options: map[string]interface{}{
					"threads": 100,
					"plugins": "./tools/plugins.txt",
				},
			},
		},
	},
}

// ExecutePipeline runs all tools in a pipeline sequentially
func (s *Server) ExecutePipeline(jobID, pipelineID string) error {
	pipeline, exists := PipelinesRegistry[pipelineID]
	if !exists {
		return fmt.Errorf("pipeline '%s' not found", pipelineID)
	}

	fmt.Printf("[Pipeline] Starting %s (ID: %s)\n", pipeline.Name, jobID)

	for i, stage := range pipeline.Tools {
		fmt.Printf("[Pipeline] Stage %d/%d: %s\n", i+1, len(pipeline.Tools), stage.ToolID)
		// Pipeline execution logic here
		time.Sleep(100 * time.Millisecond) // Placeholder
	}

	fmt.Printf("[Pipeline] Completed: %s\n", pipeline.Name)
	return nil
}
