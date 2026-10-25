package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

func (r *runtime) readLoginKey(all bool) (string, error) {
	var b []byte
	var err error
	if file, ok := r.in.(*os.File); !all && ok && term.IsTerminal(int(file.Fd())) {
		b, err = term.ReadPassword(int(file.Fd()))
		fmt.Fprintln(r.errOut)
	} else if all {
		b, err = io.ReadAll(io.LimitReader(r.in, 8193))
	} else {
		// Pressing Enter completes a login without waiting for EOF.
		b, err = bufio.NewReader(io.LimitReader(r.in, 8193)).ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			err = nil
		}
	}
	if err != nil {
		return "", err
	}
	if len(b) > 8192 {
		return "", errors.New("API key input too long")
	}
	key := strings.TrimSpace(string(b))
	if key == "" || strings.ContainsAny(key, "\r\n\t ") {
		return "", errors.New("请粘贴一个 API Key；获取地址：https://weread.qq.com/r/weread-skills；脚本可使用 weread-cli login --key-stdin")
	}
	return key, nil
}
