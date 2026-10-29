"""Сборка отдельного Windows-приложения; Python нужен только разработчику."""
from pathlib import Path
import argparse
import hashlib
import json
import os
import shutil
import subprocess

root = Path(__file__).resolve().parent
parser = argparse.ArgumentParser()
parser.add_argument('--version', default='0.1.0-dev')
parser.add_argument('--bootstrap', type=Path, default=root/'local/bootstrap.json')
parser.add_argument('--output-dir', type=Path, default=root/'bin')
args = parser.parse_args()
go = shutil.which('go') or str(root/'.tools/go/bin/go.exe')
bootstrap = json.loads(args.bootstrap.read_text(encoding='utf-8')) if args.bootstrap.exists() else {}
shared = os.environ.get('FSSP_SHARED_PATH', bootstrap.get('shared_settings', ''))
admin = os.environ.get('FSSP_ADMIN_SID', bootstrap.get('admin_sid', ''))
if not shared or not admin:
    raise SystemExit('Задайте FSSP_SHARED_PATH/FSSP_ADMIN_SID или локальный bootstrap JSON')

def run(*parts):
    subprocess.run([go, *parts], cwd=root, check=True)

run('run', 'github.com/akavel/rsrc@v0.10.2', '-manifest', 'build/app.manifest', '-ico', 'assets/app.ico', '-o', 'cmd/fssp/resource.syso', '-arch', 'amd64')
out = args.output_dir.resolve()
out.mkdir(parents=True,exist_ok=True)
# Компоновщик Go принимает значения -X в кавычках, включая пробелы и обратные слеши.
flags = '-s -w -H windowsgui '
for name, value in [('main.SharedSettings',shared), ('github.com/chassehiboux/FSSPReestrParser/internal/platform.AdminSID',admin), ('github.com/chassehiboux/FSSPReestrParser/internal/update.Version',args.version)]:
    if '"' in value or "'" in value or '\n' in value:
        raise SystemExit('Недопустимое значение параметра сборки')
    flags += '-X "'+name+'='+value+'" '
target = out/'FSSPReestrParser-windows-amd64.exe'
run('build','-trimpath','-ldflags',flags,'-o',str(target),'./cmd/fssp')
digest = hashlib.sha256(target.read_bytes()).hexdigest()
(out/'checksums.txt').write_text(f'{digest}  {target.name}\n',encoding='utf-8')
print(f'Готово: {target} ({target.stat().st_size / 1048576:.1f} МБ)')
