// In-memory filesystem polyfill for Go WASM.
// Must be loaded BEFORE wasm_exec.js (which checks `if (!globalThis.fs)`).
// Implements the Node.js fs API subset that Go's syscall/js layer requires.

"use strict";

(() => {
    // File data store: path → Uint8Array
    const files = new Map();

    // File descriptor table: fd → {path, data, pos, flags}
    const fds = new Map();
    let nextFd = 10; // 0=stdin, 1=stdout, 2=stderr reserved

    // stdout/stderr buffers
    let stdoutBuf = "";
    let stderrBuf = "";

    const encoder = new TextEncoder();
    const decoder = new TextDecoder();

    // O_ flags (match Node.js / POSIX)
    const O_RDONLY = 0;
    const O_WRONLY = 1;
    const O_RDWR = 2;
    const O_CREAT = 64;
    const O_TRUNC = 512;
    const O_APPEND = 1024;
    const O_EXCL = 128;
    const O_DIRECTORY = 65536;

    function enoent(path) {
        const err = new Error("ENOENT: no such file or directory, open '" + path + "'");
        err.code = "ENOENT";
        return err;
    }

    function enosys() {
        const err = new Error("not implemented");
        err.code = "ENOSYS";
        return err;
    }

    function makeStat(size, isDir, isFifo) {
        // mode: 16895=dir, 33206=regular file, 4480=FIFO (010600)
        const mode = isDir ? 16895 : (isFifo ? 4480 : 33206);
        return {
            dev: 0, ino: 0, mode: mode,
            nlink: 1, uid: 0, gid: 0, rdev: 0, size: size,
            blksize: 4096, blocks: Math.ceil(size / 512),
            atimeMs: Date.now(), mtimeMs: Date.now(), ctimeMs: Date.now(), birthtimeMs: Date.now(),
            isDirectory() { return isDir; },
            isFile() { return !isDir && !isFifo; },
            isBlockDevice() { return false; },
            isCharacterDevice() { return false; },
            isSymbolicLink() { return false; },
            isFIFO() { return !!isFifo; },
            isSocket() { return false; },
        };
    }

    // Directories implied by file paths
    function dirExists(path) {
        if (path === "/" || path === "." || path === "") return true;
        const prefix = path.endsWith("/") ? path : path + "/";
        for (const key of files.keys()) {
            if (key.startsWith(prefix)) return true;
        }
        return false;
    }

    globalThis.fs = {
        constants: { O_WRONLY, O_RDWR, O_CREAT, O_TRUNC, O_APPEND, O_EXCL, O_DIRECTORY },

        writeSync(fd, buf) {
            if (fd === 1) {
                stdoutBuf += decoder.decode(buf);
                const nl = stdoutBuf.lastIndexOf("\n");
                if (nl !== -1) {
                    console.log(stdoutBuf.substring(0, nl));
                    stdoutBuf = stdoutBuf.substring(nl + 1);
                }
                return buf.length;
            }
            if (fd === 2) {
                stderrBuf += decoder.decode(buf);
                const nl = stderrBuf.lastIndexOf("\n");
                if (nl !== -1) {
                    console.warn(stderrBuf.substring(0, nl));
                    stderrBuf = stderrBuf.substring(nl + 1);
                }
                return buf.length;
            }
            // Regular file fd
            const entry = fds.get(fd);
            if (!entry) return 0;
            const data = typeof buf === "string" ? encoder.encode(buf) : buf;
            // Expand file data if needed
            const needed = entry.pos + data.length;
            if (needed > entry.data.length) {
                const newData = new Uint8Array(needed);
                newData.set(entry.data);
                entry.data = newData;
            }
            entry.data.set(data, entry.pos);
            entry.pos += data.length;
            files.set(entry.path, entry.data.slice(0, Math.max(entry.data.length, needed)));
            return data.length;
        },

        write(fd, buf, offset, length, position, callback) {
            if (fd === 1 || fd === 2) {
                // stdout/stderr: handle offset/length
                const slice = buf.slice(offset, offset + length);
                const n = this.writeSync(fd, slice);
                callback(null, n);
                return;
            }
            const entry = fds.get(fd);
            if (!entry) { callback(enoent("<fd:" + fd + ">")); return; }
            if (position !== null && position !== undefined) {
                entry.pos = position;
            }
            const data = buf.slice(offset, offset + length);
            const needed = entry.pos + data.length;
            if (needed > entry.data.length) {
                const newData = new Uint8Array(needed);
                newData.set(entry.data);
                entry.data = newData;
            }
            entry.data.set(data, entry.pos);
            entry.pos += data.length;
            // Update stored file
            files.set(entry.path, entry.data.slice(0, Math.max(files.get(entry.path)?.length || 0, needed)));
            callback(null, data.length);
        },

        open(path, flags, mode, callback) {
            const creating = (flags & O_CREAT) !== 0;
            const truncating = (flags & O_TRUNC) !== 0;

            let data = files.get(path);
            if (!data && !creating) {
                callback(enoent(path));
                return;
            }
            if (!data || truncating) {
                data = new Uint8Array(0);
                files.set(path, data);
            }

            const fd = nextFd++;
            fds.set(fd, {
                path: path,
                data: new Uint8Array(data), // copy so reads/writes don't conflict
                pos: (flags & O_APPEND) ? data.length : 0,
                flags: flags,
            });
            callback(null, fd);
        },

        read(fd, buffer, offset, length, position, callback) {
            const entry = fds.get(fd);
            if (!entry) { callback(enoent("<fd:" + fd + ">")); return; }
            if (position !== null && position !== undefined) {
                entry.pos = position;
            }
            const available = entry.data.length - entry.pos;
            const toRead = Math.min(length, available);
            if (toRead <= 0) {
                callback(null, 0);
                return;
            }
            buffer.set(entry.data.subarray(entry.pos, entry.pos + toRead), offset);
            entry.pos += toRead;
            callback(null, toRead);
        },

        close(fd, callback) {
            const entry = fds.get(fd);
            if (entry) {
                // Flush written data back to file store
                files.set(entry.path, entry.data.slice(0, entry.pos > (files.get(entry.path)?.length || 0) ? entry.pos : (files.get(entry.path)?.length || entry.data.length)));
                fds.delete(fd);
            }
            callback(null);
        },

        stat(path, callback) {
            const data = files.get(path);
            if (data) {
                // /dev/fd/ paths report as non-regular (FIFO/pipe) so Go's
                // join code generation detects them as process substitutions
                const isFifo = path.startsWith("/dev/fd/");
                callback(null, makeStat(data.length, false, isFifo));
                return;
            }
            if (dirExists(path)) {
                callback(null, makeStat(0, true));
                return;
            }
            callback(enoent(path));
        },

        lstat(path, callback) {
            this.stat(path, callback);
        },

        fstat(fd, callback) {
            if (fd === 1 || fd === 2) {
                // stdout/stderr are character devices
                callback(null, makeStat(0, false));
                return;
            }
            const entry = fds.get(fd);
            if (!entry) { callback(enoent("<fd:" + fd + ">")); return; }
            callback(null, makeStat(entry.data.length, false));
        },

        mkdir(path, perm, callback) {
            // Directories are implicit — just succeed
            callback(null);
        },

        unlink(path, callback) {
            files.delete(path);
            callback(null);
        },

        rmdir(path, callback) {
            callback(null);
        },

        rename(from, to, callback) {
            const data = files.get(from);
            if (!data) { callback(enoent(from)); return; }
            files.set(to, data);
            files.delete(from);
            callback(null);
        },

        truncate(path, length, callback) {
            let data = files.get(path);
            if (!data) { callback(enoent(path)); return; }
            if (length < data.length) {
                files.set(path, data.slice(0, length));
            }
            callback(null);
        },

        ftruncate(fd, length, callback) {
            const entry = fds.get(fd);
            if (!entry) { callback(enoent("<fd:" + fd + ">")); return; }
            if (length < entry.data.length) {
                entry.data = entry.data.slice(0, length);
            }
            callback(null);
        },

        readdir(path, callback) {
            const prefix = path.endsWith("/") ? path : path + "/";
            const entries = new Set();
            for (const key of files.keys()) {
                if (key.startsWith(prefix)) {
                    const rest = key.substring(prefix.length);
                    const name = rest.split("/")[0];
                    if (name) entries.add(name);
                }
            }
            callback(null, Array.from(entries));
        },

        utimes(path, atime, mtime, callback) { callback(null); },
        chmod(path, mode, callback) { callback(null); },
        chown(path, uid, gid, callback) { callback(null); },
        fchmod(fd, mode, callback) { callback(null); },
        fchown(fd, uid, gid, callback) { callback(null); },
        lchown(path, uid, gid, callback) { callback(null); },
        link(path, link, callback) { callback(enosys()); },
        readlink(path, callback) { callback(enosys()); },
        symlink(path, link, callback) { callback(enosys()); },
        fsync(fd, callback) { callback(null); },
    };

    // Also fix process.cwd — Go needs it
    if (!globalThis.process) globalThis.process = {};
    globalThis.process.cwd = () => "/";
    globalThis.process.chdir = () => {};

    // Expose helper for JS to write files into the virtual FS
    globalThis._fsWriteFile = (path, contents) => {
        const data = typeof contents === "string" ? encoder.encode(contents) : contents;
        files.set(path, new Uint8Array(data));
    };

    // Expose helper to read files from the virtual FS
    globalThis._fsReadFile = (path) => {
        const data = files.get(path);
        if (!data) return null;
        return decoder.decode(data);
    };

    // Expose helper to list files
    globalThis._fsListFiles = () => Array.from(files.keys());
})();
