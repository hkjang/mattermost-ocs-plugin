package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pkg/errors"
)

func main() {
	manifest, err := findManifest()
	if err != nil {
		panic("failed to find manifest: " + err.Error())
	}

	pluginDir := filepath.Join("dist", manifest.Id)
	if err := stagePluginFiles(pluginDir, manifest); err != nil {
		panic("failed to stage plugin files: " + err.Error())
	}
	bundlePath := filepath.Join("dist", fmt.Sprintf("%s-%s.tar.gz", manifest.Id, manifest.Version))
	if err := packagePlugin(pluginDir, bundlePath); err != nil {
		panic("failed to package plugin bundle: " + err.Error())
	}

	fmt.Printf("plugin built at: %s\n", bundlePath)
}

func findManifest() (*model.Manifest, error) {
	_, manifestFilePath, err := model.FindManifest(".")
	if err != nil {
		return nil, errors.Wrap(err, "failed to find manifest in current working directory")
	}
	manifestFile, err := os.Open(manifestFilePath) //nolint:gosec
	if err != nil {
		return nil, errors.Wrap(err, "failed to open manifest file")
	}
	defer manifestFile.Close()

	manifest := model.Manifest{}
	if err = json.NewDecoder(manifestFile).Decode(&manifest); err != nil {
		return nil, errors.Wrap(err, "failed to decode manifest file")
	}

	return &manifest, nil
}

func stagePluginFiles(pluginDir string, manifest *model.Manifest) error {
	if manifest == nil {
		return errors.New("manifest is nil")
	}
	if err := os.RemoveAll(pluginDir); err != nil {
		return errors.Wrap(err, "failed to reset dist plugin directory")
	}
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		return errors.Wrap(err, "failed to create dist plugin directory")
	}

	manifestBytes, err := json.MarshalIndent(manifest, "", "    ")
	if err != nil {
		return errors.Wrap(err, "failed to encode manifest for dist")
	}

	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), manifestBytes, 0o644); err != nil {
		return errors.Wrap(err, "failed to write dist plugin manifest")
	}

	if err := copyDirIfExists("assets", filepath.Join(pluginDir, "assets")); err != nil {
		return err
	}
	if err := copyDirIfExists("public", filepath.Join(pluginDir, "public")); err != nil {
		return err
	}
	if manifest.HasServer() {
		if err := copyDirIfExists(filepath.Join("server", "dist"), filepath.Join(pluginDir, "server", "dist")); err != nil {
			return err
		}
	}
	if manifest.HasWebapp() {
		if err := copyDirIfExists(filepath.Join("webapp", "dist"), filepath.Join(pluginDir, "webapp", "dist")); err != nil {
			return err
		}
	}

	return nil
}

func copyDirIfExists(sourceDir, destinationDir string) error {
	info, err := os.Stat(sourceDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return errors.Wrapf(err, "failed to stat %s", sourceDir)
	}
	if !info.IsDir() {
		return errors.Errorf("%s is not a directory", sourceDir)
	}

	return filepath.WalkDir(sourceDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		relativePath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return errors.Wrapf(err, "failed to compute relative path for %s", path)
		}
		targetPath := filepath.Join(destinationDir, relativePath)

		if entry.IsDir() {
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return errors.Wrapf(err, "failed to create %s", targetPath)
			}
			return nil
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return errors.Wrapf(err, "failed to create parent directory for %s", targetPath)
		}
		return copyFile(path, targetPath)
	})
}

func copyFile(sourcePath, destinationPath string) error {
	sourceFile, err := os.Open(sourcePath) //nolint:gosec
	if err != nil {
		return errors.Wrapf(err, "failed to open %s", sourcePath)
	}
	defer sourceFile.Close()

	destinationFile, err := os.Create(destinationPath)
	if err != nil {
		return errors.Wrapf(err, "failed to create %s", destinationPath)
	}
	defer destinationFile.Close()

	if _, err := io.Copy(destinationFile, sourceFile); err != nil {
		return errors.Wrapf(err, "failed to copy %s to %s", sourcePath, destinationPath)
	}

	return nil
}

func packagePlugin(pluginDir, bundlePath string) error {
	if err := os.Remove(bundlePath); err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "failed to remove previous bundle")
	}

	bundleFile, err := os.Create(bundlePath)
	if err != nil {
		return errors.Wrap(err, "failed to create bundle file")
	}
	defer bundleFile.Close()

	gzipWriter := gzip.NewWriter(bundleFile)
	defer gzipWriter.Close()

	tarWriter := tar.NewWriter(gzipWriter)
	defer tarWriter.Close()

	baseDir := filepath.Dir(pluginDir)
	return filepath.WalkDir(pluginDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		info, err := entry.Info()
		if err != nil {
			return errors.Wrap(err, "failed to stat bundle entry")
		}

		relativePath, err := filepath.Rel(baseDir, path)
		if err != nil {
			return errors.Wrap(err, "failed to compute bundle path")
		}
		archivePath := filepath.ToSlash(relativePath)
		if entry.IsDir() && !strings.HasSuffix(archivePath, "/") {
			archivePath += "/"
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return errors.Wrap(err, "failed to create tar header")
		}
		header.Name = archivePath
		header.Uid = 0
		header.Gid = 0
		header.Uname = ""
		header.Gname = ""
		header.Mode = fileModeForArchivePath(archivePath, entry.IsDir())

		if err := tarWriter.WriteHeader(header); err != nil {
			return errors.Wrap(err, "failed to write tar header")
		}

		if entry.IsDir() {
			return nil
		}

		sourceFile, err := os.Open(path) //nolint:gosec
		if err != nil {
			return errors.Wrap(err, "failed to open bundle source file")
		}
		defer sourceFile.Close()

		if _, err := io.Copy(tarWriter, sourceFile); err != nil {
			return errors.Wrap(err, "failed to copy file into tar archive")
		}

		return nil
	})
}

func fileModeForArchivePath(path string, isDir bool) int64 {
	if isDir {
		return 0o755
	}
	if strings.Contains(path, "/server/dist/") {
		return 0o755
	}
	return 0o644
}
