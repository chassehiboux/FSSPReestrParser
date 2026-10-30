Option Explicit

' Только встроенные Windows API: ссылки Tools / References не требуются.
Private Declare PtrSafe Function InternetOpenW Lib "wininet" (ByVal Agent As LongPtr, ByVal AccessType As Long, ByVal Proxy As LongPtr, ByVal Bypass As LongPtr, ByVal Flags As Long) As LongPtr
Private Declare PtrSafe Function InternetOpenUrlW Lib "wininet" (ByVal Session As LongPtr, ByVal Url As LongPtr, ByVal Headers As LongPtr, ByVal HeaderLength As Long, ByVal Flags As Long, ByVal Context As LongPtr) As LongPtr
Private Declare PtrSafe Function InternetSetOptionW Lib "wininet" (ByVal Handle As LongPtr, ByVal OptionId As Long, ByRef Value As Long, ByVal ValueSize As Long) As Long
Private Declare PtrSafe Function HttpQueryInfoW Lib "wininet" (ByVal Handle As LongPtr, ByVal Info As Long, ByRef Buffer As Any, ByRef BufferSize As Long, ByVal Index As LongPtr) As Long
Private Declare PtrSafe Function InternetReadFile Lib "wininet" (ByVal Handle As LongPtr, ByRef Buffer As Any, ByVal BufferSize As Long, ByRef BytesRead As Long) As Long
Private Declare PtrSafe Function InternetCloseHandle Lib "wininet" (ByVal Handle As LongPtr) As Long

Private Session As LongPtr
Private Request As LongPtr
Private OutputFile As Integer
Private TextBody As Object
Private LimitBytes As Double
Public Done As Boolean
Public Failure As String
Public Received As Double
Public Total As Double

Public Sub Start(ByVal Url As String, ByVal Target As String, ByVal Maximum As Double, Optional ByVal ExpectedSize As Double = 0)
    On Error GoTo Failed
    Done = False
    Failure = vbNullString
    Received = 0
    Total = ExpectedSize
    LimitBytes = Maximum
    If Len(Target) > 0 Then
        If Len(Dir$(Target)) > 0 Then Err.Raise 5, , "Временный файл уже существует"
        OutputFile = FreeFile
        Open Target For Binary Access Write Lock Read Write As #OutputFile
    Else
        Set TextBody = CreateObject("ADODB.Stream")
        TextBody.Type = 1
        TextBody.Open
    End If
    Session = InternetOpenW(StrPtr("FSSPReestrParser-Excel-Launcher/1.1"), 0, 0, 0, 0)
    If Session = 0 Then Err.Raise 5, , "Не удалось открыть соединение: Windows " & Err.LastDllError
    Dim Timeout As Long
    Timeout = 15000
    If InternetSetOptionW(Session, 2, Timeout, 4) = 0 Then Err.Raise 5, , "Не удалось задать время подключения"
    If InternetSetOptionW(Session, 5, Timeout, 4) = 0 Then Err.Raise 5, , "Не удалось задать время отправки"
    If InternetSetOptionW(Session, 6, Timeout, 4) = 0 Then Err.Raise 5, , "Не удалось задать время получения"
    ' RELOAD, NO_CACHE_WRITE, NO_UI, NO_COOKIES: не использовать кэш или cookies.
    Dim Headers As String
    Headers = "Accept: */*" & vbCrLf
    Request = InternetOpenUrlW(Session, StrPtr(Url), StrPtr(Headers), Len(Headers), &H84080200, 0)
    If Request = 0 Then Err.Raise 5, , "Ошибка соединения: Windows " & Err.LastDllError
    Dim Status As Long, Size As Long
    Size = 4
    If HttpQueryInfoW(Request, &H20000013, Status, Size, 0) = 0 Then Err.Raise 5, , "Не удалось прочитать ответ сервера"
    If Status <> 200 Then Err.Raise 5, , "Сервер вернул HTTP " & CStr(Status)
    Dim Header As String, HeaderSize As Double
    Header = String$(64, vbNullChar)
    Size = Len(Header) * 2
    If HttpQueryInfoW(Request, 5, ByVal StrPtr(Header), Size, 0) <> 0 Then HeaderSize = CDbl(Left$(Header, Size \ 2))
    If Total > 0 And HeaderSize > 0 And Total <> HeaderSize Then Err.Raise 5, , "Размер файла отличается от указанного в выпуске"
    If Total = 0 Then Total = HeaderSize
    If Total > LimitBytes Then Err.Raise 5, , "Превышен допустимый размер скачивания"
    Exit Sub
Failed:
    FinishError Err.Description
End Sub

Public Sub Poll()
    If Done Then Exit Sub
    On Error GoTo Failed
    Dim Data() As Byte, Count As Long
    ReDim Data(0 To 32767)
    If InternetReadFile(Request, Data(0), 32768, Count) = 0 Then Err.Raise 5, , "Ошибка получения данных: Windows " & Err.LastDllError
    If Count = 0 Then
        If Total > 0 And Received <> Total Then Err.Raise 5, , "Файл скачан не полностью"
        Done = True
        CloseTransfer
        Exit Sub
    End If
    If Received + Count > LimitBytes Then Err.Raise 5, , "Превышен допустимый размер скачивания"
    ReDim Preserve Data(0 To Count - 1)
    If OutputFile <> 0 Then
        Put #OutputFile, , Data
    Else
        TextBody.Write Data
    End If
    Received = Received + Count
    Exit Sub
Failed:
    FinishError Err.Description
End Sub

Public Function ResponseText() As String
    If Not Done Or Len(Failure) > 0 Or TextBody Is Nothing Then Err.Raise 5, , "Ответ ещё не получен"
    TextBody.Position = 0
    TextBody.Type = 2
    TextBody.Charset = "utf-8"
    ResponseText = TextBody.ReadText
End Function

Public Sub Cancel()
    If Done Then Exit Sub
    FinishError "Операция отменена пользователем"
End Sub

Private Sub FinishError(ByVal Message As String)
    Failure = Message
    Done = True
    CloseTransfer
End Sub

Private Sub CloseTransfer()
    On Error Resume Next
    If OutputFile <> 0 Then Close #OutputFile
    OutputFile = 0
    If Request <> 0 Then InternetCloseHandle Request
    Request = 0
    If Session <> 0 Then InternetCloseHandle Session
    Session = 0
End Sub

Private Sub Class_Terminate()
    CloseTransfer
    On Error Resume Next
    If Not TextBody Is Nothing Then TextBody.Close
End Sub
