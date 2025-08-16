package main

import (
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"

	webp "github.com/chai2010/webp"
	"github.com/spf13/cobra"
)

type convertOptions struct {
	quality        float32
	lossless       bool
	overwrite      bool
	deleteOriginal bool
	recursive      bool
	workers        int
	directory      string
}

var (
	opts = convertOptions{
		quality:   80,
		lossless:  false,
		overwrite: false,
		recursive: false,
		workers:   runtime.NumCPU(),
		directory: ".",
	}
)

var rootCmd = &cobra.Command{
	Use:   "image-convert",
	Short: "Convert images to WebP format",
	Long: `A fast and efficient tool to convert various image formats to WebP.
Supports JPEG, PNG, GIF, BMP, TIFF formats and converts them to WebP with configurable quality and options.`,
	RunE: runConvert,
}

func init() {
	// Quality flag
	rootCmd.Flags().Float32VarP(&opts.quality, "quality", "q", 80, "WebP quality (0-100)")

	// Boolean flags
	rootCmd.Flags().BoolVarP(&opts.lossless, "lossless", "l", false, "Use lossless WebP encoding")
	rootCmd.Flags().BoolVarP(&opts.overwrite, "overwrite", "o", false, "Overwrite existing .webp files if present")
	rootCmd.Flags().BoolVarP(&opts.deleteOriginal, "delete-original", "d", false, "Delete the original image after successful conversion")
	rootCmd.Flags().BoolVarP(&opts.recursive, "recursive", "r", false, "Recurse into subdirectories")

	// Other flags
	rootCmd.Flags().IntVarP(&opts.workers, "workers", "w", runtime.NumCPU(), "Number of concurrent workers")
	rootCmd.Flags().StringVarP(&opts.directory, "directory", "D", ".", "Directory to process (default: current directory)")

	// Mark directory flag as required
	rootCmd.MarkFlagRequired("directory")
}

func runConvert(cmd *cobra.Command, args []string) error {
	// Validate quality range
	if opts.quality < 0 || opts.quality > 100 {
		return fmt.Errorf("quality must be between 0 and 100")
	}

	// Validate workers
	if opts.workers < 1 {
		return fmt.Errorf("workers must be at least 1")
	}

	files, err := collectImageFiles(opts.directory, opts.recursive)
	if err != nil {
		return fmt.Errorf("error collecting files: %w", err)
	}

	if len(files) == 0 {
		fmt.Println("No images found to convert.")
		return nil
	}

	total := len(files)
	fmt.Printf("Found %d image(s). Converting to WebP...\n", total)

	jobs := make(chan string)
	var wg sync.WaitGroup

	type result struct {
		path string
		err  error
	}
	results := make(chan result)

	workerCount := opts.workers
	if workerCount < 1 {
		workerCount = 1
	}

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range jobs {
				err := convertOne(path, opts)
				results <- result{path: path, err: err}
			}
		}()
	}

	go func() {
		for _, f := range files {
			jobs <- f
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	converted := 0
	failed := 0
	for r := range results {
		if r.err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "[FAIL]	%s: %v\n", r.path, r.err)
		} else {
			converted++
			fmt.Printf("[OK]	%s\n", r.path)
		}
	}

	fmt.Printf("Done. Converted: %d, Failed: %d\n", converted, failed)
	return nil
}

func collectImageFiles(root string, recursive bool) ([]string, error) {
	allowed := map[string]struct{}{
		".jpg":  {},
		".jpeg": {},
		".png":  {},
		".gif":  {},
		".bmp":  {},
		".tif":  {},
		".tiff": {},
		".webp": {}, // we will skip converting these but allow discovery for filtering
	}

	var paths []string
	if recursive {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				// Skip hidden directories like .git, .cache, etc.
				if isHidden(d.Name()) && path != "." {
					return filepath.SkipDir
				}
				return nil
			}
			if isHidden(d.Name()) {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(d.Name()))
			if _, ok := allowed[ext]; ok {
				// Skip already webp
				if ext == ".webp" {
					return nil
				}
				paths = append(paths, path)
			}
			return nil
		})
		return paths, err
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || isHidden(e.Name()) {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if _, ok := allowed[ext]; ok && ext != ".webp" {
			paths = append(paths, filepath.Join(root, e.Name()))
		}
	}
	return paths, nil
}

func convertOne(inputPath string, opts convertOptions) error {
	in, err := os.Open(inputPath)
	if err != nil {
		return err
	}

	img, _, err := image.Decode(in)
	if err != nil {
		return fmt.Errorf("decode: %w", err)
	}

	outPath := makeOutPath(inputPath)
	if !opts.overwrite {
		if _, statErr := os.Stat(outPath); statErr == nil {
			return errors.New("destination exists (use -overwrite to replace)")
		}
	}

	// Ensure output directory exists
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}

	tmpPath := outPath + ".tmp"
	out, err := os.Create(tmpPath)
	if err != nil {
		return err
	}

	encOpts := &webp.Options{Lossless: opts.lossless, Quality: opts.quality}
	if err := webp.Encode(out, img, encOpts); err != nil {
		out.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("encode webp: %w", err)
	}
	if err := out.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}

	if err := os.Rename(tmpPath, outPath); err != nil {
		os.Remove(tmpPath)
		return err
	}

	in.Close()

	if opts.deleteOriginal {
		if err := os.Remove(inputPath); err != nil {
			return fmt.Errorf("failed to delete original file %s: %w", inputPath, err)
		}
	}

	return nil
}

func makeOutPath(input string) string {
	dir := filepath.Dir(input)
	base := filepath.Base(input)
	name := strings.TrimSuffix(base, filepath.Ext(base))
	return filepath.Join(dir, name+".webp")
}

func isHidden(name string) bool {
	return strings.HasPrefix(name, ".")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
