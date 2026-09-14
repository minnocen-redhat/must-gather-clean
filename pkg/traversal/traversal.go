package traversal

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sync"

	"k8s.io/klog/v2"
)

type Traverser interface {
	Traverse()
}

type FileWalker struct {
	inputPath     string
	workerCount   int
	workers       []QueueProcessor
	workerFactory func(int) QueueProcessor
}

// Traverse preserves the original process-exit behavior for callers of the
// public traversal API. New callers that need transactional error handling
// should use TraverseWithError.
func (w *FileWalker) Traverse() {
	if err := w.TraverseWithError(); err != nil {
		var fileErr *fileProcessingError
		if errors.As(err, &fileErr) {
			klog.Exitf("failed to process %s due to %v", fileErr.path, fileErr.cause)
		}
		klog.Exitf("unexpected error: %v", err)
	}
}

// TraverseWithError starts processing the must-gather directory and returns
// all errors encountered while walking or processing files.
func (w *FileWalker) TraverseWithError() error {
	wg := sync.WaitGroup{}
	errorCh := make(chan error, w.workerCount)
	queue := make(chan workerInput, w.workerCount)
	w.workers = make([]QueueProcessor, w.workerCount)
	for i := 0; i < w.workerCount; i++ {
		w.workers[i] = w.workerFactory(i + 1)
		wg.Add(1)
		go func(i int, queue chan workerInput, errorCh chan error) {
			w.workers[i].ProcessQueue(queue, errorCh)
			wg.Done()
		}(i, queue, errorCh)
	}

	var processingErrors []error
	errorWg := sync.WaitGroup{}
	errorWg.Add(1)
	go func(errorCh <-chan error) {
		defer errorWg.Done()
		for err := range errorCh {
			processingErrors = append(processingErrors, err)
		}
	}(errorCh)

	err := filepath.WalkDir(w.inputPath, func(path string, dirEntry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !dirEntry.IsDir() {
			// the rest of the logic expects the path to be relative to the input dir root, if it fails we assume it is already relative
			relPath, err := filepath.Rel(w.inputPath, path)
			if err != nil {
				queue <- workerInput(path)
			} else {
				queue <- workerInput(relPath)
			}
		}

		return nil
	})

	if err != nil {
		close(queue)
		wg.Wait()
		close(errorCh)
		errorWg.Wait()
		return fmt.Errorf("failed to traverse the directory structure: %w", err)
	}

	close(queue)
	wg.Wait()

	// once all the workers have exited close the error channel and wait for the exit goroutine to complete.
	close(errorCh)
	errorWg.Wait()

	if len(processingErrors) == 0 {
		return nil
	}
	wrapped := make([]error, 0, len(processingErrors))
	for _, processingErr := range processingErrors {
		var fileErr *fileProcessingError
		if errors.As(processingErr, &fileErr) {
			wrapped = append(wrapped, fmt.Errorf("failed to process %s: %w", fileErr.path, fileErr.cause))
		} else {
			wrapped = append(wrapped, processingErr)
		}
	}
	return errors.Join(wrapped...)
}

func NewParallelFileWalker(inputPath string, workerCount int, workerFactory func(id int) QueueProcessor) *FileWalker {
	return &FileWalker{
		inputPath:     inputPath,
		workerCount:   workerCount,
		workerFactory: workerFactory,
	}
}
