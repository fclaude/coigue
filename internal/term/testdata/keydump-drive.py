import os, pty, select, subprocess, sys, time, struct, fcntl, termios
from concurrent.futures import ThreadPoolExecutor

HARNESS = sys.argv[1]
OUT = sys.argv[2]
ESC = b'\x1b'

cases = []  # list of list-of-chunks
def add(*chunks):
    cases.append([c if isinstance(c, bytes) else c.encode() for c in chunks])

for b in range(0x80):
    add(bytes([b]))
for b in range(0x20, 0x7f):
    add(ESC + bytes([b]))
add(ESC + ESC); add(ESC + ESC + b'[A'); add(ESC + b'\x01'); add(ESC + b'\x7f'); add(ESC + b'\r'); add(ESC + b'\t'); add(ESC + 'é'.encode())
for b in range(0x40, 0x7b):
    add(ESC + b'O' + bytes([b]))
for b in range(0x21, 0x7f):
    add(ESC + b'[' + bytes([b]))
for b in b'ABCDE':
    add(ESC + b'[[' + bytes([b]))
add(ESC + b'[[F')
for n in list(range(0, 40)) + [99, 255, 256, 1000]:
    add(ESC + b'[%d~' % n)
    add(ESC + b'[%d;5~' % n if n < 40 else ESC + b'[1~')
for n in (1, 2, 3, 5, 6, 11, 15, 17, 20, 21, 23, 24, 25, 26, 28, 29, 31, 34):
    for mod in range(1, 10):
        add(ESC + b'[%d;%d~' % (n, mod))
    add(ESC + b'[%d;5:3~' % n); add(ESC + b'[%d;5:2~' % n); add(ESC + b'[%d;2:1~' % n)
for x in b'ABCDFHPQRS':
    for mod in (1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 16, 17, 33, 255):
        add(ESC + b'[1;%d%c' % (mod, x))
    add(ESC + b'[1;3:3%c' % x); add(ESC + b'[1;3:2%c' % x); add(ESC + b'[5%c' % x); add(ESC + b'[2%c' % x)
for cp in (97, 65, 13, 27, 9, 127, 32, 10, 0, 57358, 57363, 57376, 57398, 57399, 57408, 57409, 57413, 57414, 57417, 57420, 57421, 57426, 57427, 57428, 57440, 57441, 57443, 57447, 57449, 57454, 57455, 233, 30028, 0x1F980, 0x10FFFF, 0x110000, 0xD800):
    add(ESC + b'[%du' % cp)
    add(ESC + b'[%d;2u' % cp)
    add(ESC + b'[%d;5u' % cp)
    add(ESC + b'[%d;1:3u' % cp)
for s in (b'97:65;2u', b'97:65;3u', b'97:65:66;2u', b'9;2u', b'9;3u', b'97;130u', b'97;66u', b'97;2:1u', b'1;2u'):
    add(ESC + b'[' + s)
add(ESC + b'[200~hello' + ESC + b'[201~')
add(ESC + b'[200~a\nb' + ESC + b'[201~')
add(ESC + b'[200~unterminated')
add(ESC + b'[200~' + ESC + b'[201~')
add(ESC + b'[200~\xff' + ESC + b'[201~')
for seq in (b'[M !!', b'[M#!!', b'[M\x20\x20\x20', b'[<0;20;10M', b'[<0;20;10m', b'[<35;1;1M', b'[<64;5;5M', b'[<0;0;1M', b'[<300;1;1M', b'[I', b'[O', b'[20;10R', b'[0;10R', b'[?1;2c', b'[?1u', b'[?', b'[32;30;40;M', b'[32;0;40;M', b'[40;1;1M', b'[0;1;1M'):
    add(ESC + seq)
for s in ('é', '界', '🦀', 'ß', 'İ', 'Σ', 'ǅ', 'Ａ'):
    add(s)
    add(s.encode()[:1], s.encode()[1:])
add(b'\xc3'); add(b'\x80'); add(b'\xff'); add(b'\xc3\x28'); add(b'\xe2\x82a'); add(b'\xf0\x28\x8c\xbc'); add(b'\xe2\x28\xa1'); add(b'\xf8'); add(b'\xc0\x80'); add(b'\xed\xa0\x80')
add(ESC + b'[]a'); add(ESC + b'Oxa'); add(ESC + b'[999~a'); add(ESC + b'[1;3Xa'); add(ESC + b'[;a'); add(ESC + b'[;5A'); add(ESC + b'[\x01a'); add(ESC + b'[ ' + b'a')
add(b'ab'); add(b'a' + ESC + b'[Ab'); add(ESC + b'OP' + ESC + b'OQ'); add(b'A' + b'\x1bB')
add(b'\x1b[1;5A\x1b[1;3C\r\t\x7f')
add(ESC, b'b'); add(ESC + b'[', b'A'); add(ESC + b'[1;', b'3D'); add(ESC + b'[200~hel', b'lo' + ESC + b'[201~')
add(ESC, b'[A'); add(ESC + b'O', b'P'); add(ESC + b'[15', b'~'); add(ESC + b'[1', b';5A'); add(ESC + b'[<0;20', b';10M')
add(ESC, ESC); add(ESC, ESC + b'[A'); add(b'\xf0\x9f', b'\xa6\x80')
add(b'x' + ESC, b'y')
print(len(cases), 'cases', file=sys.stderr)

def run(chunks):
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 24, 80, 0, 0))
    proc = subprocess.Popen([HARNESS], stdin=slave, stdout=slave, stderr=slave, close_fds=True)
    os.close(slave)
    buf = b''
    def pump(timeout):
        nonlocal buf
        if select.select([master], [], [], timeout)[0]:
            try:
                buf += os.read(master, 65536)
            except OSError:
                return False
        return True
    end = time.time() + 5
    while b'READY' not in buf and time.time() < end:
        pump(0.05)
    for i, chunk in enumerate(chunks):
        if i:
            time.sleep(0.08)
        os.write(master, chunk)
    end = time.time() + 6
    while b'DONE' not in buf and time.time() < end:
        if not pump(0.05):
            break
    try:
        proc.wait(timeout=2)
    except subprocess.TimeoutExpired:
        proc.kill(); proc.wait(); buf += b'\nTIMEOUT\n'
    os.close(master)
    lines = buf.decode(errors='replace').replace('\r\n', '\n').split('\n')
    events = [l for l in lines if l.startswith(('key', 'paste', 'resize', 'other', 'TIMEOUT'))]
    return events

def label(chunks):
    return '+'.join(c.hex() for c in chunks)

with ThreadPoolExecutor(max_workers=12) as pool:
    results = list(pool.map(run, cases))
with open(OUT, 'w') as f:
    for chunks, events in zip(cases, results):
        f.write(label(chunks) + '\t' + ';'.join(events) + '\n')
print('wrote', OUT, file=sys.stderr)
