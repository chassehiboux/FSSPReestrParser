package registry

import (
	"context"
	"io"
	"os"
)

func copyFile(ctx context.Context, src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	complete := false
	defer func() {
		if !complete {
			os.Remove(dst)
		}
	}()
	buf := make([]byte, 128*1024)
	for {
		if err = ctx.Err(); err != nil {
			break
		}
		n, e := in.Read(buf)
		if n > 0 {
			if _, err = out.Write(buf[:n]); err != nil {
				break
			}
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			err = e
			break
		}
	}
	if err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err != nil {
		return err
	}
	complete = closeErr == nil
	return closeErr
}
