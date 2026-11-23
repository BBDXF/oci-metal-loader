import os,re,sys,glob,subprocess,html
CW,CH=10,21
def parse(line):
    fg=(216,222,233);bg=(46,52,64);bold=False;out=[];i=0
    for m in re.finditer(r'\x1b\[([0-9;]*)m|(.)',line):
        if m.group(2) is None:
            p=[int(x) if x else 0 for x in m.group(1).split(';')];j=0
            while j<len(p):
                c=p[j]
                if c==0: fg=(216,222,233);bg=(46,52,64);bold=False
                elif c==1: bold=True
                elif c==38 and p[j+1]==2: fg=tuple(p[j+2:j+5]);j+=4
                elif c==48 and p[j+1]==2: bg=tuple(p[j+2:j+5]);j+=4
                elif c==7: fg,bg=bg,fg
                j+=1
        else: out.append((m.group(2),fg,bg,bold))
    return out
os.makedirs('/tmp/opencode',exist_ok=True)
for f in sorted(glob.glob(sys.argv[1]+'/*.ans')):
    rows=[parse(l) for l in open(f,encoding='utf8').read().split('\n') if l!='' or False][:]
    W=max(len(r) for r in rows);H=len(rows)
    s=[f'<svg xmlns="http://www.w3.org/2000/svg" width="{W*CW+20}" height="{H*CH+20}"><rect width="100%" height="100%" fill="#2e3440"/><g transform="translate(10,10)" font-family="JetBrainsMono Nerd Font Mono" font-size="16.6">']
    for y,r in enumerate(rows):
        for x,(ch,fg,bg,b) in enumerate(r):
            if bg!=(46,52,64): s.append(f'<rect x="{x*CW}" y="{y*CH}" width="{CW}" height="{CH}" fill="rgb{bg}"/>')
        for x,(ch,fg,bg,b) in enumerate(r):
            if ch!=' ': s.append(f'<text x="{x*CW}" y="{y*CH+15}" fill="rgb{fg}"{" font-weight=\"bold\"" if b else ""}>{html.escape(ch)}</text>')
    s.append('</g></svg>')
    out=os.path.basename(f)[:-4]
    open(f'/tmp/opencode/{out}.svg','w').write('\n'.join(s))
    subprocess.run(['rsvg-convert','-o',os.path.join(sys.argv[2],out+'.png'),f'/tmp/opencode/{out}.svg'],check=True)
