/***************************************************************
 *
 * Copyright (C) 2024, Morgridge Institute for Research
 *
 * Licensed under the Apache License, Version 2.0 (the "License"); you
 * may not use this file except in compliance with the License.  You may
 * obtain a copy of the License at
 *
 *    http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 ***************************************************************/

package client

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
)

type packerBehavior int

type packedError struct{ Value error }

type atomicError struct {
	err atomic.Value
}

type autoUnpacker struct {
	atomicError
	Behavior     packerBehavior
	detectedType packerBehavior
	destDir      string
	buffer       bytes.Buffer
	writer       io.WriteCloser
}

type autoPacker struct {
	atomicError
	Behavior   packerBehavior
	srcDir     string
	reader     io.ReadCloser
	srcDirSize atomic.Int64
	srcDirDone atomic.Int64
}

const (
	autoBehavior packerBehavior = iota
	tarBehavior
	tarGZBehavior
	tarXZBehavior
	zipBehavior

	defaultBehavior packerBehavior = tarGZBehavior
)

func newAutoUnpacker(destdir string, behavior packerBehavior) *autoUnpacker {
	aup := &autoUnpacker{
		Behavior: behavior,
		destDir:  destdir,
	}
	aup.err.Store(packedError{})
	if os := runtime.GOOS; os == "windows" {
		aup.StoreError(errors.New("Auto-unpacking functionality not supported on Windows"))
	}
	return aup
}

func newAutoPacker(srcdir string, behavior packerBehavior) *autoPacker {
	ap := &autoPacker{
		Behavior: behavior,
		srcDir:   srcdir,
	}
	ap.err.Store(packedError{})
	if os := runtime.GOOS; os == "windows" {
		ap.StoreError(errors.New("Auto-unpacking functionality not supported on Windows"))
	} else {
		go ap.calcDirectorySize()
	}
	return ap
}

func GetBehavior(behaviorName string) (packerBehavior, error) {
	switch behaviorName {
	case "auto":
		return autoBehavior, nil
	case "tar":
		return tarBehavior, nil
	case "tar.gz":
		return tarGZBehavior, nil
	case "tar.xz":
		return tarXZBehavior, nil
	case "zip":
		return zipBehavior, nil
	}
	return autoBehavior, errors.Errorf("Unknown value for 'pack' parameter: %v", behaviorName)
}

func (aup *atomicError) Error() error {
	value := aup.err.Load()
	if err, ok := value.(packedError); ok {
		return err.Value
	}
	return nil
}

func (aup *atomicError) StoreError(err error) {
	aup.err.CompareAndSwap(packedError{}, packedError{Value: err})
}

func (aup *autoUnpacker) detect() (packerBehavior, error) {
	currentBytes := aup.buffer.Bytes()
	// gzip streams start with 1F 8B
	if len(currentBytes) >= 2 && bytes.Equal(currentBytes[0:2], []byte{0x1F, 0x8B}) {
		return tarGZBehavior, nil
	}
	// xz streams start with FD 37 7A 58 5A 00
	if len(currentBytes) >= 6 && bytes.Equal(currentBytes[0:6], []byte{0xFD, 0x37, 0x7A, 0x58, 0x5A, 0x00}) {
		return tarXZBehavior, nil
	}
	// tar files, at offset 257, have bytes 75 73 74 61 72
	if len(currentBytes) >= (257+5) && bytes.Equal(currentBytes[257:257+5], []byte{0x75, 0x73, 0x74, 0x61, 0x72}) {
		return tarBehavior, nil
	}
	// zip files start with 50 4B 03 04
	if len(currentBytes) >= 4 && bytes.Equal(currentBytes[0:4], []byte{0x50, 0x4B, 0x03, 0x04}) {
		return zipBehavior, nil
	}
	if len(currentBytes) > (257 + 5) {
		return autoBehavior, errors.New("Unable to detect pack type")
	}
	return autoBehavior, nil
}

func writeRegFile(path string, mode int64, reader io.Reader) error {
	fp, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, fs.FileMode(mode))
	if err != nil {
		return err
	}
	defer fp.Close()
	_, err = io.Copy(fp, reader)
	return err
}

type autoPackerHelper struct {
	curFp io.Reader
	ap    *autoPacker
}

func (aph *autoPackerHelper) Read(p []byte) (n int, err error) {
	n, err = aph.curFp.Read(p)
	aph.ap.srcDirDone.Add(int64(n))
	return
}

func (ap *autoPacker) readRegFile(path string, writer io.Writer) error {
	fp, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	aph := &autoPackerHelper{fp, ap}
	defer fp.Close()
	_, err = io.Copy(writer, aph)
	return err
}

func (ap *autoPacker) calcDirectorySize() {
	err := filepath.WalkDir(ap.srcDir, func(path string, dent fs.DirEntry, err error) error {
		if err != nil {
			log.Warningln("Error when walking source directory to calculate size:", err.Error())
			return filepath.SkipDir
		}
		if dent.Type().IsRegular() {
			fi, err := dent.Info()
			if err != nil {
				log.Warningln("Error when stat'ing file:", err.Error())
				return nil
			}
			ap.srcDirSize.Add(fi.Size())
		}
		return nil
	})
	if err != nil {
		log.Warningln("Failure when calculating the source directory size:", err.Error())
	}
}

func (ap *autoPacker) Size() int64 {
	return ap.srcDirSize.Load()
}

func (ap *autoPacker) BytesComplete() int64 {
	return ap.srcDirDone.Load()
}

func (ap *autoPacker) pack(tw *tar.Writer, gz *gzip.Writer, pwriter *io.PipeWriter) {
	srcPrefix := filepath.Clean(ap.srcDir) + "/"
	defer pwriter.Close()
	err := filepath.WalkDir(ap.srcDir, func(path string, dent fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		path = filepath.Clean(path)
		if !strings.HasPrefix(path, srcPrefix) {
			return nil
		}
		tarName := path[len(srcPrefix):]
		if tarName == "" || tarName[0] == '/' {
			return errors.New("Invalid path provided by filepath.Walk")
		}

		fi, err := dent.Info()
		if err != nil {
			return err
		}
		link := ""
		if (fi.Mode() & fs.ModeSymlink) == fs.ModeSymlink {
			link, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}
		hdr, err := tar.FileInfoHeader(fi, link)
		if err != nil {
			return err
		}
		hdr.Name = tarName
		if err = tw.WriteHeader(hdr); err != nil {
			return err
		}
		if fi.Mode().IsRegular() {
			if err = ap.readRegFile(path, tw); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		ap.StoreError(err)
		return
	}
	if err = tw.Close(); err != nil {
		ap.StoreError(err)
		return
	}
	if gz != nil {
		if err = gz.Close(); err != nil {
			ap.StoreError(err)
			return
		}
	}
	pwriter.CloseWithError(io.EOF)
}

func (aup *autoUnpacker) unpack(tr *tar.Reader, preader *io.PipeReader) {
	log.Debugln("Beginning unpacker of type", aup.Behavior)
	defer preader.Close()
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			preader.CloseWithError(err)
			break
		}
		if err != nil {
			aup.StoreError(err)
			break
		}
		destPath := filepath.Join(aup.destDir, hdr.Name)
		destPath = filepath.Clean(destPath)
		if !strings.HasPrefix(destPath, aup.destDir) {
			aup.StoreError(errors.New("Tarfile contains object outside the destination directory"))
			break
		}
		switch hdr.Typeflag {
		case tar.TypeReg:
			err = writeRegFile(destPath, hdr.Mode, tr)
			if err != nil {
				aup.StoreError(errors.Wrapf(err, "Failure when unpacking file to %v", destPath))
				return
			}
		case tar.TypeLink:
			targetPath := filepath.Join(aup.destDir, hdr.Linkname)
			if !strings.HasPrefix(targetPath, aup.destDir) {
				aup.StoreError(errors.New("Tarfile contains hard link target outside the destination directory"))
				return
			}
			if err = os.Link(targetPath, destPath); err != nil {
				aup.StoreError(errors.Wrapf(err, "Failure when unpacking hard link to %v", destPath))
				return
			}
		case tar.TypeSymlink:
			if err = os.Symlink(hdr.Linkname, destPath); err != nil {
				aup.StoreError(errors.Wrapf(err, "Failure when creating symlink at %v", destPath))
				return
			}
		case tar.TypeChar:
			log.Debugln("Ignoring tar entry of type character device at", destPath)
		case tar.TypeBlock:
			log.Debugln("Ignoring tar entry of type block device at", destPath)
		case tar.TypeDir:
			if err = os.MkdirAll(destPath, fs.FileMode(hdr.Mode)); err != nil {
				aup.StoreError(errors.Wrapf(err, "Failure when creating directory at %v", destPath))
				return
			}
		case tar.TypeFifo:
			log.Debugln("Ignoring tar entry of type FIFO at", destPath)
		case 103: // pax_global_header, written by git archive.  OK to ignore
		default:
			log.Debugln("Ignoring unknown tar entry of type", hdr.Typeflag)
		}
	}
}

func (aup *autoUnpacker) configure() (err error) {
	preader, pwriter := io.Pipe()
	bufDrained := make(chan error)
	// gzip.NewReader function will block reading from the pipe.
	// Asynchronously write the contents of the buffer from a separate goroutine;
	// Note we don't return from configure() until the buffer is consumed.
	go func() {
		_, err := aup.buffer.WriteTo(pwriter)
		bufDrained <- err
	}()
	var tarUnpacker *tar.Reader
	switch aup.detectedType {
	case autoBehavior:
		return errors.New("Configure invoked before file type is known")
	case tarBehavior:
		tarUnpacker = tar.NewReader(preader)
	case tarGZBehavior:
		gzStreamer, err := gzip.NewReader(preader)
		if err != nil {
			return err
		}
		tarUnpacker = tar.NewReader(gzStreamer)
	case tarXZBehavior:
		return errors.New("tar.xz has not yet been implemented")
	case zipBehavior:
		return errors.New("zip file support has not yet been implemented")
	}
	go aup.unpack(tarUnpacker, preader)
	if err = <-bufDrained; err != nil {
		return errors.Wrap(err, "Failed to copy byte buffer to unpacker")
	}
	aup.writer = pwriter
	return nil
}

func (ap *autoPacker) configure() (err error) {
	preader, pwriter := io.Pipe()
	if ap.Behavior == autoBehavior {
		ap.Behavior = defaultBehavior
	}
	var tarPacker *tar.Writer
	var streamer *gzip.Writer
	switch ap.Behavior {
	case tarBehavior:
		tarPacker = tar.NewWriter(pwriter)
	case tarGZBehavior:
		streamer = gzip.NewWriter(pwriter)
		tarPacker = tar.NewWriter(streamer)
	case tarXZBehavior:
		return errors.New("tar.xz has not yet been implemented")
	case zipBehavior:
		return errors.New("zip file support has not yet been implemented")
	}
	go ap.pack(tarPacker, streamer, pwriter)
	ap.reader = preader
	return nil
}

func (ap *autoPacker) Read(p []byte) (n int, err error) {
	if ap.srcDir == "" {
		err = errors.New("AutoPacker object must be initialized via NewPacker")
		return
	}

	if err = ap.Error(); err != nil {
		if ap.reader != nil {
			ap.reader.Close()
		}
		return
	}

	if ap.reader == nil {
		if err = ap.configure(); err != nil {
			return
		}
	}

	n, readerErr := ap.reader.Read(p)
	if err = ap.Error(); err != nil {
		return
	}
	return n, readerErr
}

func (aup *autoUnpacker) Write(p []byte) (n int, err error) {
	if aup.destDir == "" {
		err = errors.New("AutoUnpacker object must be initialized via NewAutoUnpacker")
		return
	}
	err = aup.Error()
	if err != nil {
		if aup.writer != nil {
			aup.writer.Close()
		}
		return
	}

	if aup.detectedType == autoBehavior {
		if n, err = aup.buffer.Write(p); err != nil {
			return
		}
		if aup.detectedType, err = aup.detect(); aup.detectedType == autoBehavior {
			n = len(p)
			return
		} else if err = aup.configure(); err != nil {
			return
		}
		// Note the byte buffer already consumed all the bytes, hence return here.
		return len(p), nil
	} else if aup.writer == nil {
		if err = aup.configure(); err != nil {
			return
		}
	}
	n, writerErr := aup.writer.Write(p)
	if err = aup.Error(); err != nil {
		return n, err
	} else if writerErr != nil {
		if writerErr == io.EOF {
			return len(p), nil
		}
	}
	return n, writerErr
}

func (aup autoUnpacker) Close() error {
	if aup.buffer.Len() > 0 {
		aup.StoreError(errors.New("AutoUnpacker was closed prior to detecting any file type; no bytes were written"))
	}
	if aup.Behavior == autoBehavior {
		aup.StoreError(errors.New("AutoUnpacker was closed prior to any bytes written"))
	}
	return aup.Error()
}

func (ap *autoPacker) Close() error {
	if ap.reader != nil {
		return ap.reader.Close()
	}
	return nil
}
