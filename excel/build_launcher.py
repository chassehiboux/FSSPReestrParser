"""Сборка настоящих импортируемых компонентов VBA через установленный Excel.

Исходники хранятся в UTF-8; VBE экспортирует .bas/.cls/.frm в нативной ANSI-кодировке.
Параметры безопасности Office не изменяются.
"""
from pathlib import Path
import argparse
import json
import zipfile
import win32com.client

ROOT=Path(__file__).resolve().parent
parser=argparse.ArgumentParser()
parser.add_argument('--output-dir',type=Path,default=ROOT.parent/'output'/'excel-launcher')
args=parser.parse_args()
out=args.output_dir.resolve()
out.mkdir(parents=True,exist_ok=True)
app=win32com.client.DispatchEx('Excel.Application')
app.Visible=False
app.DisplayAlerts=False
book=None
try:
    book=app.Workbooks.Add()
    project=book.VBProject
    project.References.AddFromGuid('{662901FC-6951-4854-9EB2-D9A2570F2B2E}',5,1)
    for name,kind,filename in [('mFSSPLauncher',1,'Launcher.bas'),('cFSSPDownload',2,'Download.cls')]:
        component=project.VBComponents.Add(kind)
        component.Name=name
        component.CodeModule.AddFromString((ROOT/'launcher'/filename).read_text(encoding='utf-8'))
        component.Export(str(out/(name+('.bas' if kind==1 else '.cls'))))
    form=json.loads((ROOT/'launcher'/'form.json').read_text(encoding='utf-8'))
    component=project.VBComponents.Add(3)
    component.Name=form['name']
    component.Properties('Caption').Value=form['caption']
    component.Properties('Width').Value=form['width']
    component.Properties('Height').Value=form['height']
    component.Properties('StartUpPosition').Value=1
    controls={}
    for spec in form['controls']:
        parent=controls[spec['parent']] if 'parent' in spec else component.Designer
        control=parent.Controls.Add(spec['type'],spec['name'],True)
        controls[spec['name']]=control
        for prop in ['left','top','width','height']:
            setattr(control,prop.capitalize(),spec[prop])
        control.Caption=spec['caption']
        control.Font.Name='Segoe UI'
        control.Font.Size=spec['size']
        control.Font.Bold=spec.get('bold',False)
        if 'fill' in spec:control.BackColor=spec['fill'];control.BackStyle=1
        if spec['name']=='cmdCancel':control.Cancel=True
    component.CodeModule.AddFromString((ROOT/'launcher'/'FormCode.vba').read_text(encoding='utf-8'))
    component.Export(str(out/(form['name']+'.frm')))
    sheet=book.Worksheets(1)
    sheet.Name='Запуск'
    sheet.Range('A2').Value='Реестры ФССП'
    sheet.Range('A2').Font.Size=20
    sheet.Range('A4').Value='Нажмите кнопку: приложение будет найдено или скачано с GitHub.'
    sheet.Range('A5').Value='Последующие обновления выполняются самим приложением.'
    sheet.Columns('A').ColumnWidth=90
    button=sheet.Shapes.AddShape(5,20,120,340,48)
    button.TextFrame.Characters().Text='Запустить реестры ФССП'
    button.OnAction='StartFSSPReestrParser'
    book.SaveAs(str(out/'Запуск_реестров_ФССП.xlsm'),52)
    # Выполнение функции одновременно компилирует обычный модуль и проверяет BCrypt.
    sample=out/'sha256-test.txt'
    sample.write_bytes(b'abc')
    digest=app.Run("'"+book.Name+"'!FSSPHash",str(sample))
    assert digest=='ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad',digest
    sample.unlink()
    book.Close(False)
    book=None
finally:
    if book is not None:book.Close(False)
    app.Quit()
readme=ROOT/'launcher'/'README.md'
if readme.exists():(out/'README.md').write_text(readme.read_text(encoding='utf-8'),encoding='utf-8')
with zipfile.ZipFile(out/'Excel_запуск_ФССП.zip','w',zipfile.ZIP_DEFLATED) as archive:
    for p in out.iterdir():
        if p.suffix.lower() in {'.bas','.cls','.frm','.frx','.xlsm','.md'}:archive.write(p,p.name)
print('Компоненты VBA, UserForm и книга запуска собраны:',out)
