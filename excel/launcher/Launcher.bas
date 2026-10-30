Option Explicit

Private Declare PtrSafe Sub Sleep Lib "kernel32" (ByVal Milliseconds As Long)
Private Declare PtrSafe Function BCryptOpenAlgorithmProvider Lib "bcrypt" (ByRef Handle As LongPtr, ByVal Algorithm As LongPtr, ByVal Provider As LongPtr, ByVal Flags As Long) As Long
Private Declare PtrSafe Function BCryptGetProperty Lib "bcrypt" (ByVal Handle As LongPtr, ByVal Property As LongPtr, ByRef Output As Any, ByVal Size As Long, ByRef ResultSize As Long, ByVal Flags As Long) As Long
Private Declare PtrSafe Function BCryptCreateHash Lib "bcrypt" (ByVal Provider As LongPtr, ByRef Hash As LongPtr, ByRef HashObject As Any, ByVal ObjectSize As Long, ByVal Secret As LongPtr, ByVal SecretSize As Long, ByVal Flags As Long) As Long
Private Declare PtrSafe Function BCryptHashData Lib "bcrypt" (ByVal Hash As LongPtr, ByRef Buffer As Any, ByVal Size As Long, ByVal Flags As Long) As Long
Private Declare PtrSafe Function BCryptFinishHash Lib "bcrypt" (ByVal Hash As LongPtr, ByRef Output As Any, ByVal Size As Long, ByVal Flags As Long) As Long
Private Declare PtrSafe Function BCryptDestroyHash Lib "bcrypt" (ByVal Hash As LongPtr) As Long
Private Declare PtrSafe Function BCryptCloseAlgorithmProvider Lib "bcrypt" (ByVal Provider As LongPtr, ByVal Flags As Long) As Long

Public Const FSSP_ASSET As String = "FSSPReestrParser-windows-amd64.exe"
Private Const RELEASE_API As String = "https://api.github.com/repos/chassehiboux/FSSPReestrParser/releases/latest"
Private Const RELEASE_ROOT As String = "https://github.com/chassehiboux/FSSPReestrParser/releases/download/"
Private Const REG_PATH As String = "HKCU\Software\FSSPReestrParser\ExecutablePath"
Private Running As Boolean

Public Sub StartFSSPReestrParser()
    If Running Then Exit Sub
    Running = True
    On Error GoTo Failed
    Dim View As New frmFSSPLauncher
    View.Show vbModal
    Unload View
    Running = False
    Exit Sub
Failed:
    Running = False
    MsgBox "Не удалось открыть запуск реестров ФССП: " & Err.Description, vbExclamation, "Реестры ФССП"
End Sub

Public Function FSSPInstallFolder() As String
    FSSPInstallFolder = Environ$("LOCALAPPDATA") & "\FSSPReestrParser\app"
End Function

Public Function FindFSSPApplication() As String
    Dim Shell As Object, Fso As Object, Candidate As String, Folders As Collection, Folder As Variant
    Set Shell = CreateObject("WScript.Shell")
    Set Fso = CreateObject("Scripting.FileSystemObject")
    On Error Resume Next
    Candidate = Shell.RegRead(REG_PATH)
    On Error GoTo 0
    If IsFSSPExecutable(Candidate) Then FindFSSPApplication = Candidate: Exit Function
    Set Folders = New Collection
    Folders.Add FSSPInstallFolder()
    If Len(ThisWorkbook.Path) > 0 Then Folders.Add ThisWorkbook.Path
    On Error Resume Next
    Folders.Add Shell.SpecialFolders("Desktop")
    Folders.Add CreateObject("Shell.Application").NameSpace("shell:Downloads").Self.Path
    On Error GoTo 0
    For Each Folder In Folders
        Candidate = Fso.BuildPath(CStr(Folder), FSSP_ASSET)
        If IsFSSPExecutable(Candidate) Then FindFSSPApplication = Candidate: Exit Function
        Candidate = Fso.BuildPath(CStr(Folder), "FSSPReestrParser.exe")
        If IsFSSPExecutable(Candidate) Then FindFSSPApplication = Candidate: Exit Function
    Next Folder
End Function

Public Function IsFSSPExecutable(ByVal FilePath As String) As Boolean
    Dim File As Integer, Signature(0 To 3) As Byte, Machine(0 To 1) As Byte, Offset As Long
    On Error GoTo Finished
    If Len(FilePath) = 0 Then Exit Function
    If Not CreateObject("Scripting.FileSystemObject").FileExists(FilePath) Then Exit Function
    File = FreeFile
    Open FilePath For Binary Access Read As #File
    If LOF(File) < 128 Then GoTo Finished
    Get #File, 1, Signature
    If Signature(0) <> &H4D Or Signature(1) <> &H5A Then GoTo Finished
    Get #File, &H3D, Offset
    If Offset < 64 Or Offset > LOF(File) - 24 Then GoTo Finished
    Get #File, Offset + 1, Signature
    If Signature(0) <> &H50 Or Signature(1) <> &H45 Or Signature(2) <> 0 Or Signature(3) <> 0 Then GoTo Finished
    Get #File, Offset + 5, Machine
    IsFSSPExecutable = Machine(0) = &H64 And Machine(1) = &H86
Finished:
    On Error Resume Next
    If File <> 0 Then Close #File
End Function

Public Function FSSPHash(ByVal FilePath As String, Optional ByVal View As Object) As String
    Dim Algorithm As LongPtr, Hash As LongPtr, ObjectSize As Long, ResultSize As Long
    Dim HashObject() As Byte, Digest(0 To 31) As Byte, Buffer() As Byte
    Dim File As Integer, Remaining As Long, Count As Long, I As Long, Text As String, Failure As String
    On Error GoTo Failed
    If BCryptOpenAlgorithmProvider(Algorithm, StrPtr("SHA256"), 0, 0) <> 0 Then Err.Raise 5, , "SHA-256 недоступен"
    If BCryptGetProperty(Algorithm, StrPtr("ObjectLength"), ObjectSize, 4, ResultSize, 0) <> 0 Then Err.Raise 5, , "Не удалось подготовить SHA-256"
    ReDim HashObject(0 To ObjectSize - 1)
    If BCryptCreateHash(Algorithm, Hash, HashObject(0), ObjectSize, 0, 0, 0) <> 0 Then Err.Raise 5, , "Не удалось создать SHA-256"
    File = FreeFile
    Open FilePath For Binary Access Read As #File
    Remaining = LOF(File)
    Do While Remaining > 0
        Count = Remaining
        If Count > 65536 Then Count = 65536
        ReDim Buffer(0 To Count - 1)
        Get #File, , Buffer
        If BCryptHashData(Hash, Buffer(0), Count, 0) <> 0 Then Err.Raise 5, , "Ошибка расчёта SHA-256"
        Remaining = Remaining - Count
        DoEvents
        If Not View Is Nothing Then
            If View.Cancelled Then Err.Raise 18, , "Операция отменена пользователем"
        End If
    Loop
    If BCryptFinishHash(Hash, Digest(0), 32, 0) <> 0 Then Err.Raise 5, , "Ошибка завершения SHA-256"
    For I = 0 To 31
        Text = Text & Right$("0" & Hex$(Digest(I)), 2)
    Next I
    FSSPHash = LCase$(Text)
    GoTo Finished
Failed:
    Failure = Err.Description
Finished:
    On Error Resume Next
    If File <> 0 Then Close #File
    If Hash <> 0 Then BCryptDestroyHash Hash
    If Algorithm <> 0 Then BCryptCloseAlgorithmProvider Algorithm, 0
    On Error GoTo 0
    If Len(Failure) > 0 Then Err.Raise 5, , Failure
End Function

Private Function MatchText(ByVal Text As String, ByVal Pattern As String) As String
    Dim Re As Object, Matches As Object
    Set Re = CreateObject("VBScript.RegExp")
    Re.Pattern = Pattern
    Re.IgnoreCase = True
    Re.MultiLine = True
    Set Matches = Re.Execute(Text)
    If Matches.Count <> 1 Then Err.Raise 5, , "Не удалось прочитать данные выпуска GitHub"
    MatchText = Matches(0).SubMatches(0)
End Function

Private Sub WaitDownload(ByVal Request As cFSSPDownload, ByVal View As Object)
    Dim Started As Double, LastPaint As Double, Elapsed As Double
    Started = Timer
    Do While Not Request.Done
        DoEvents
        If View.Cancelled Then Request.Cancel
        Elapsed = Timer - Started
        If Elapsed < 0 Then Elapsed = Elapsed + 86400#
        If Elapsed > 600 Then Request.Cancel: Err.Raise 5, , "Превышено время ожидания скачивания"
        If Timer - LastPaint >= 0.1 Or Timer < LastPaint Then
            View.ShowProgress Request.Received, Request.Total, Elapsed
            LastPaint = Timer
        End If
        Sleep 25
    Loop
    Elapsed = Timer - Started
    If Elapsed < 0 Then Elapsed = Elapsed + 86400#
    View.ShowProgress Request.Received, Request.Total, Elapsed
    If Len(Request.Failure) > 0 Then Err.Raise 5, , Request.Failure
    If View.Cancelled Then Err.Raise 18, , "Операция отменена пользователем"
End Sub

Private Function GetText(ByVal Url As String, ByVal Maximum As Double, ByVal View As Object) As String
    Dim Request As New cFSSPDownload
    Request.Start Url, vbNullString, Maximum
    WaitDownload Request, View
    GetText = Request.ResponseText
End Function

Public Function InstallFSSP(ByVal View As Object) As String
    Dim Fso As Object, Folder As String, Target As String, Temporary As String, LockPath As String
    Dim LockFile As Integer, Locked As Boolean, Json As String, Tag As String, Size As Double
    Dim BaseUrl As String, Checksum As String, Text As String, Request As cFSSPDownload, Failure As String
    On Error GoTo Failed
    Set Fso = CreateObject("Scripting.FileSystemObject")
    Folder = FSSPInstallFolder()
    If Len(Environ$("LOCALAPPDATA")) = 0 Then Err.Raise 5, , "Не найдена локальная папка пользователя"
    If Not Fso.FolderExists(Fso.GetParentFolderName(Folder)) Then Fso.CreateFolder Fso.GetParentFolderName(Folder)
    If Not Fso.FolderExists(Folder) Then Fso.CreateFolder Folder
    LockPath = Folder & "\install.lock"
    LockFile = FreeFile
    On Error Resume Next
    Open LockPath For Binary Access Read Write Lock Read Write As #LockFile
    Locked = Err.Number = 0
    Err.Clear
    On Error GoTo Failed
    If Not Locked Then Err.Raise 5, , "Установка приложения уже выполняется в другом окне Excel"
    Target = Folder & "\" & FSSP_ASSET
    If IsFSSPExecutable(Target) Then InstallFSSP = Target: GoTo Finished
    View.SetStage "Проверка последнего выпуска GitHub…"
    Json = GetText(RELEASE_API, 2097152, View)
    Tag = MatchText(Json, """tag_name""\s*:\s*""(v[0-9]+\.[0-9]+\.[0-9]+)""")
    Size = CDbl(MatchText(Json, """name""\s*:\s*""FSSPReestrParser-windows-amd64\.exe""[\s\S]*?""size""\s*:\s*([0-9]+)"))
    If Size < 128 Or Size > 268435456# Then Err.Raise 5, , "Некорректный размер приложения в выпуске"
    BaseUrl = RELEASE_ROOT & Tag & "/"
    View.SetStage "Получение контрольной суммы…"
    Text = GetText(BaseUrl & "checksums.txt", 65536, View)
    Checksum = LCase$(MatchText(Text, "^([a-f0-9]{64})\s+\*?FSSPReestrParser-windows-amd64\.exe\s*$"))
    Temporary = Folder & "\" & Fso.GetTempName & ".part"
    Set Request = New cFSSPDownload
    View.SetStage "Скачивание приложения " & Tag & "…"
    Request.Start BaseUrl & FSSP_ASSET, Temporary, 268435456#, Size
    WaitDownload Request, View
    View.SetStage "Проверка скачанного приложения…"
    If FSSPHash(Temporary, View) <> Checksum Then Err.Raise 5, , "Контрольная сумма приложения не совпала. Файл не будет запущен"
    If Not IsFSSPExecutable(Temporary) Then Err.Raise 5, , "Скачанный файл не является приложением Windows x64"
    If View.Cancelled Then Err.Raise 18, , "Операция отменена пользователем"
    If Fso.FileExists(Target) Then Fso.MoveFile Target, Target & ".invalid-" & Format$(Now, "yyyymmdd-hhnnss")
    Fso.MoveFile Temporary, Target
    Temporary = vbNullString
    CreateObject("WScript.Shell").RegWrite REG_PATH, Target, "REG_SZ"
    InstallFSSP = Target
    GoTo Finished
Failed:
    Failure = Err.Description
Finished:
    On Error Resume Next
    If Not Request Is Nothing Then If Not Request.Done Then Request.Cancel
    If Locked Then Close #LockFile
    If Locked Then If Fso.FileExists(LockPath) Then Fso.DeleteFile LockPath
    If Len(Temporary) > 0 Then If Fso.FileExists(Temporary) Then Fso.DeleteFile Temporary
    On Error GoTo 0
    If Len(Failure) > 0 Then Err.Raise 5, , Failure
End Function

Public Sub RunFSSPExecutable(ByVal FilePath As String)
    If Not IsFSSPExecutable(FilePath) Then Err.Raise 5, , "Приложение не найдено или повреждено"
    CreateObject("WScript.Shell").Run """" & FilePath & """", 1, False
End Sub
