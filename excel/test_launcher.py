"""Интеграционная проверка VBA в Excel с локальным HTTP-сервером.

Тестовая копия модулей использует временную папку и не запускает EXE,
не обращается к GitHub и не изменяет реестр пользователя.
"""
from pathlib import Path
import hashlib
import http.server
import json
import struct
import tempfile
import threading
import time
import win32com.client

ROOT = Path(__file__).resolve().parent
payload = bytearray(2 * 1024 * 1024)
payload[:2] = b'MZ'
struct.pack_into('<I', payload, 60, 128)
payload[128:134] = b'PE\0\0\x64\x86'
digest = hashlib.sha256(payload).hexdigest()
mode = 'success'


class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_GET(self):
        if mode == 'http-error':
            self.send_error(503)
            return
        if self.path == '/latest':
            body = json.dumps({'tag_name': 'v9.9.9', 'assets': [{'name': 'FSSPReestrParser-windows-amd64.exe', 'size': len(payload)}]}).encode()
        elif self.path.endswith('checksums.txt'):
            body = ((digest if mode != 'bad-hash' else '0' * 64) + '  FSSPReestrParser-windows-amd64.exe\n').encode()
        else:
            body = payload
        self.send_response(200)
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        try:
            for offset in range(0, len(body), 32768):
                self.wfile.write(body[offset:offset + 32768])
                self.wfile.flush()
                if len(body) > 65536:
                    time.sleep(.012)
        except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError):
            pass


server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler)
threading.Thread(target=server.serve_forever, daemon=True).start()
url = f'http://127.0.0.1:{server.server_port}'
app = win32com.client.DispatchEx('Excel.Application')
app.Visible = False
app.DisplayAlerts = False
book = None
try:
    with tempfile.TemporaryDirectory(prefix='fssp-vba-') as folder:
        root = Path(folder)
        book = app.Workbooks.Open(str(ROOT.parent / 'output/excel-launcher/Запуск_реестров_ФССП.xlsm'))
        module = book.VBProject.VBComponents('mFSSPLauncher').CodeModule
        code = module.Lines(1, module.CountOfLines)
        code = code.replace('https://api.github.com/repos/chassehiboux/FSSPReestrParser/releases/latest', url + '/latest')
        code = code.replace('https://github.com/chassehiboux/FSSPReestrParser/releases/download/', url + '/download/')
        code = code.replace('Environ$("LOCALAPPDATA") & "\\FSSPReestrParser\\app"', '"' + str(root / 'app') + '"')
        code = code.replace('CreateObject("WScript.Shell").RegWrite REG_PATH, Target, "REG_SZ"', "' Реестр отключён только в тестовой копии")
        module.DeleteLines(1, module.CountOfLines)
        module.AddFromString(code + '''
Public Function TestInstall(ByVal AbortDownload As Boolean) As String
    Dim View As New TestView, Installed As String
    View.AbortDownload = AbortDownload
    On Error GoTo Failed
    Installed = InstallFSSP(View)
    TestInstall = "OK|" & Installed & "|" & View.Paints
    Exit Function
Failed:
    TestInstall = "ERROR|" & Err.Description & "|" & View.Paints
End Function
''')
        view = book.VBProject.VBComponents.Add(2)
        view.Name = 'TestView'
        view.CodeModule.AddFromString('''Option Explicit
Public Cancelled As Boolean
Public AbortDownload As Boolean
Public Paints As Long
Public Sub SetStage(ByVal Text As String)
End Sub
Public Sub ShowProgress(ByVal Received As Double, ByVal Total As Double, ByVal Seconds As Double)
    Paints = Paints + 1
    If AbortDownload And Total > 65536 And Received > 0 Then Cancelled = True
End Sub
''')
        for mode in ['success', 'bad-hash', 'http-error', 'cancel']:
            target = root / 'app/FSSPReestrParser-windows-amd64.exe'
            target.unlink(missing_ok=True)
            result = app.Run("'" + book.Name + "'!TestInstall", mode == 'cancel')
            if mode == 'success':
                assert result.startswith('OK|'), result
                assert hashlib.sha256(target.read_bytes()).hexdigest() == digest
                assert int(result.rsplit('|', 1)[1]) >= 5, result
            else:
                assert result.startswith('ERROR|') and not target.exists(), result
            assert not list((root / 'app').glob('*.part'))
            assert not (root / 'app/install.lock').exists()
            print(mode, result, flush=True)
finally:
    if book is not None:
        book.Close(False)
    app.Quit()
    server.shutdown()
