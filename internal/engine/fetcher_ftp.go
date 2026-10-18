package engine

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/jlaffaye/ftp"

	"github.com/xdl-app/xdl/internal/netx"
)

// DownloadFTP fetches one file over FTP with a single connection and REST resume.
// Extra ftp:// mirrors are tried in order when the current one fails.
func DownloadFTP(ctx context.Context, opt DownloadOptions) (*DownloadResult, error) {
	urls := []string{strings.TrimSpace(opt.URL)}
	for _, m := range opt.Mirrors {
		m = strings.TrimSpace(m)
		if m == "" || !strings.HasPrefix(strings.ToLower(m), "ftp://") {
			continue
		}
		urls = append(urls, m)
	}
	var last error
	for _, raw := range urls {
		res, err := downloadFTPOne(ctx, opt, raw)
		if err == nil {
			return res, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		last = err
	}
	if last == nil {
		last = NewTaskError(ErrCodeProbeFailed, "没有可用的 FTP 地址", "检查链接后重试")
	}
	return nil, last
}

func downloadFTPOne(ctx context.Context, opt DownloadOptions, raw string) (*DownloadResult, error) {
	addr, user, pass, remote, err := parseFTPURL(raw)
	if err != nil {
		return nil, NewTaskError(ErrCodeProbeFailed, err.Error(), "检查 FTP 地址")
	}
	dial, err := netx.DialFunc(opt.Proxy)
	if err != nil {
		return nil, NewTaskError(ErrCodeNetwork, err.Error(), "FTP 请改用 SOCKS5 或直连")
	}

	conn, err := ftp.Dial(addr,
		ftp.DialWithContext(ctx),
		ftp.DialWithTimeout(30*time.Second),
		ftp.DialWithDialFunc(dial),
	)
	if err != nil {
		return nil, NewTaskError(ErrCodeNetwork, err.Error(), "检查 FTP 服务器或代理")
	}
	defer conn.Quit()
	if err := conn.Login(user, pass); err != nil {
		return nil, NewTaskError(ErrCodeAuthRequired, err.Error(), "检查 FTP 用户名和密码")
	}

	size, sizeErr := conn.FileSize(remote)
	if sizeErr != nil {
		size = -1
	}
	if size > 0 {
		if err := EnsureDiskSpace(opt.Dir, size); err != nil {
			return nil, err
		}
	}

	name := opt.Name
	if name == "" {
		name = ftpLeaf(raw)
	}
	name = SanitizeFilename(name)
	if err := os.MkdirAll(opt.Dir, 0o755); err != nil {
		return nil, err
	}
	finalPath := opt.FinalPath
	partPath := opt.PartPath
	if finalPath == "" {
		finalPath, err = UniquePath(opt.Dir, name)
		if err != nil {
			return nil, err
		}
	}
	if partPath == "" {
		partPath = PartPath(finalPath)
	}

	var offset int64
	if st, serr := os.Stat(LongPath(partPath)); serr == nil {
		offset = st.Size()
	}
	if size > 0 && offset > size {
		offset = 0
		_ = os.Remove(LongPath(partPath))
	}
	if size > 0 && offset == 0 {
		if err := Preallocate(partPath, size); err != nil {
			return nil, fmt.Errorf("preallocate: %w", err)
		}
	}
	if size > 0 && offset == size {
		if err := os.Rename(LongPath(partPath), LongPath(finalPath)); err != nil && !os.IsExist(err) {
			if _, statErr := os.Stat(LongPath(finalPath)); statErr != nil {
				return nil, err
			}
		}
		return &DownloadResult{Path: finalPath, Size: size, FinalURL: raw, Name: name}, nil
	}

	var resp *ftp.Response
	if offset > 0 {
		resp, err = conn.RetrFrom(remote, uint64(offset))
	} else {
		resp, err = conn.Retr(remote)
	}
	if err != nil {
		return nil, NewTaskError(ErrCodeNetwork, err.Error(), "检查路径或服务器是否允许续传")
	}
	defer resp.Close()

	file, err := os.OpenFile(LongPath(partPath), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if offset > 0 {
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			file.Close()
			return nil, err
		}
	}
	written := offset
	buf := make([]byte, 32*1024)
	lastTick := time.Now()
	var lastDone int64
	report := func(force bool) {
		if opt.OnProgress == nil {
			return
		}
		if !force && time.Since(lastTick) < progressInterval {
			return
		}
		elapsed := time.Since(lastTick).Seconds()
		speed := int64(0)
		if elapsed > 0 {
			speed = int64(float64(written-lastDone) / elapsed)
		}
		total := size
		if total < 0 {
			total = 0
		}
		opt.OnProgress(written, total, speed)
		lastTick = time.Now()
		lastDone = written
	}
	for {
		if err := ctx.Err(); err != nil {
			file.Close()
			return nil, err
		}
		n, rerr := resp.Read(buf)
		if n > 0 {
			if opt.GlobalLim != nil {
				if err := opt.GlobalLim.WaitN(ctx, n); err != nil {
					file.Close()
					return nil, err
				}
			}
			if opt.TaskLim != nil {
				if err := opt.TaskLim.WaitN(ctx, n); err != nil {
					file.Close()
					return nil, err
				}
			}
			if _, werr := file.Write(buf[:n]); werr != nil {
				file.Close()
				return nil, werr
			}
			written += int64(n)
			report(false)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			file.Close()
			return nil, NewTaskError(ErrCodeNetwork, rerr.Error(), "FTP 传输中断，可继续任务")
		}
	}
	report(true)
	if err := file.Sync(); err != nil {
		file.Close()
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	if size > 0 && written != size {
		return nil, NewTaskError(ErrCodeNetwork, "FTP 传输长度与 SIZE 不一致", "重试或更换镜像")
	}
	if err := os.Rename(LongPath(partPath), LongPath(finalPath)); err != nil {
		return nil, fmt.Errorf("rename: %w", err)
	}
	if size <= 0 {
		size = written
	}
	return &DownloadResult{Path: finalPath, Size: size, FinalURL: raw, Name: name}, nil
}

func parseFTPURL(raw string) (addr, user, pass, remote string, err error) {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "ftp") || u.Hostname() == "" {
		return "", "", "", "", fmt.Errorf("无效的 FTP 地址")
	}
	port := u.Port()
	if port == "" {
		port = "21"
	}
	addr = u.Hostname() + ":" + port
	user, pass = "anonymous", "anonymous@"
	if u.User != nil {
		user = u.User.Username()
		if p, ok := u.User.Password(); ok {
			pass = p
		}
	}
	remote = u.Path
	if remote == "" {
		remote = "/"
	}
	if decoded, derr := url.PathUnescape(remote); derr == nil {
		remote = decoded
	}
	remote = path.Clean(remote)
	if remote == "." {
		remote = "/"
	}
	return addr, user, pass, remote, nil
}

func ftpLeaf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "download"
	}
	base := path.Base(u.Path)
	if base == "." || base == "/" || base == "" {
		return "download"
	}
	if decoded, derr := url.PathUnescape(base); derr == nil {
		base = decoded
	}
	return base
}

func (m *Manager) runFTPTask(ctx context.Context, id TaskID, gen uint64, t *Task) {
	m.mu.Lock()
	cur := m.tasks[id]
	if cur == nil || m.runGen[id] != gen {
		m.mu.Unlock()
		return
	}
	name := cur.Name
	if name == "" {
		name = ftpLeaf(cur.URL)
	}
	name = SanitizeFilename(name)
	cur.Name = name
	var claimErr error
	if cur.FinalPath == "" {
		var fp string
		fp, claimErr = m.claimPathLocked(id, cur.Dir, name)
		if claimErr == nil {
			cur.FinalPath = fp
			cur.PartPath = PartPath(fp)
		}
	} else {
		m.pathHold[cur.FinalPath] = id
	}
	opt := DownloadOptions{
		URL:       cur.URL,
		Mirrors:   append([]string(nil), cur.Mirrors...),
		Proxy:     cur.Proxy,
		Dir:       cur.Dir,
		Name:      name,
		GlobalLim: m.global,
		TaskLim:   m.taskLimiter(id, cur.LimitBps),
		FinalPath: cur.FinalPath,
		PartPath:  cur.PartPath,
		OnProgress: func(done, total, speed int64) {
			m.mu.Lock()
			if m.runGen[id] != gen {
				m.mu.Unlock()
				return
			}
			if task, ok := m.tasks[id]; ok && (task.State == StateRunning || task.State == StateResolving) {
				task.Done = done
				if total > 0 {
					task.Size = total
				}
				task.Speed = speed
				task.State = StateRunning
			}
			m.mu.Unlock()
			m.bus.Publish(Event{Type: EventTaskProgress, Data: map[string]interface{}{
				"id": id, "done": done, "total": total, "speed": speed,
			}})
		},
	}
	tcp := *cur
	m.mu.Unlock()
	if claimErr != nil {
		m.setStateIfGen(id, gen, StateFailed, NewTaskError(ErrCodeInternal, claimErr.Error(), "更换目录后重试"))
		return
	}
	_ = m.store.PutTask(&tcp)
	res, err := DownloadFTP(ctx, opt)
	m.finishDownload(ctx, id, gen, res, err)
}
