package traversal

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileWalkerReturnsProcessingErrors(t *testing.T) {
	inputDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(inputDir, "input.log"), []byte("input"), 0600))
	want := errors.New("processing failed")
	walker := NewParallelFileWalker(inputDir, 1, func(id int) QueueProcessor {
		return NewWorker(id, noOpCleaner{desiredError: &want})
	})

	err := walker.Traverse()
	require.Error(t, err)
	assert.ErrorIs(t, err, want)
}

type collectingQueueProcessor struct {
	paths []string
}

func (c *collectingQueueProcessor) ProcessQueue(queue chan workerInput, _ chan error) {
	for wf := range queue {
		c.paths = append(c.paths, string(wf))
	}
}

func TestFileWalker(t *testing.T) {
	for _, tc := range []struct {
		name           string
		inputDir       string
		expectedResult []string
	}{
		{
			name:     "basic",
			inputDir: "testfiles/test1/mg",
			expectedResult: []string{
				"nodes/another.yaml",
				"nodes/test.yaml",
				"pods/pod1/application.log",
				"pods/pod1/manifests.yaml",
				"pods/pod2/application.log",
				"pods/pod2/manifests.yaml",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			queueProc := &collectingQueueProcessor{[]string{}}
			walker := NewParallelFileWalker(tc.inputDir, 1, func(id int) QueueProcessor {
				return queueProc
			})

			walker.Traverse()

			assert.Equal(t, tc.expectedResult, queueProc.paths)
		})
	}
}

func TestFileWalkerAbsolutePathing(t *testing.T) {
	abs, err := filepath.Abs("testfiles/test1/mg")
	require.NoError(t, err)

	queueProc := &collectingQueueProcessor{[]string{}}
	walker := NewParallelFileWalker(abs, 1, func(id int) QueueProcessor {
		return queueProc
	})

	walker.Traverse()

	assert.Equal(t, []string{
		"nodes/another.yaml",
		"nodes/test.yaml",
		"pods/pod1/application.log",
		"pods/pod1/manifests.yaml",
		"pods/pod2/application.log",
		"pods/pod2/manifests.yaml",
	}, queueProc.paths)
}
