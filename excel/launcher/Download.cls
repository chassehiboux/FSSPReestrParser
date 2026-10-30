Option Explicit

Private WithEvents Http As WinHttp.WinHttpRequest
Private OutputFile As Integer
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
    End If
    Set Http = New WinHttp.WinHttpRequest
    Http.SetTimeouts 10000, 15000, 30000, 30000
    Http.Open "GET", Url, True
    Http.SetRequestHeader "User-Agent", "FSSPReestrParser-Excel-Launcher/1.0"
    Http.SetRequestHeader "Accept", "application/octet-stream, application/json"
    Http.Send
    Exit Sub
Failed:
    FinishError Err.Description
End Sub

Public Function ResponseText() As String
    If Not Done Or Len(Failure) > 0 Then Err.Raise 5, , "Ответ ещё не получен"
    ResponseText = Http.ResponseText
End Function

Public Sub Cancel()
    If Done Then Exit Sub
    FinishError "Операция отменена пользователем"
End Sub

Private Sub FinishError(ByVal Message As String)
    If Done Then Exit Sub
    Failure = Message
    Done = True
    On Error Resume Next
    If OutputFile <> 0 Then Close #OutputFile
    OutputFile = 0
    If Not Http Is Nothing Then Http.Abort
    On Error GoTo 0
End Sub

Private Sub Http_OnResponseStart(ByVal Status As Long, ByVal ContentType As String)
    If Status <> 200 Then
        FinishError "Сервер вернул HTTP " & CStr(Status)
        Exit Sub
    End If
    Dim HeaderSize As Double
    On Error Resume Next
    HeaderSize = CDbl(Http.GetResponseHeader("Content-Length"))
    On Error GoTo 0
    If Total > 0 And HeaderSize > 0 And Total <> HeaderSize Then
        FinishError "Размер файла отличается от указанного в выпуске"
        Exit Sub
    End If
    If Total = 0 Then Total = HeaderSize
    If Total > LimitBytes Then FinishError "Превышен допустимый размер скачивания"
End Sub

Private Sub Http_OnResponseDataAvailable(Data() As Byte)
    If Done Then Exit Sub
    On Error GoTo Failed
    Dim Count As Long
    Count = UBound(Data) - LBound(Data) + 1
    If Received + Count > LimitBytes Then Err.Raise 5, , "Превышен допустимый размер скачивания"
    If OutputFile <> 0 Then Put #OutputFile, , Data
    Received = Received + Count
    Exit Sub
Failed:
    FinishError Err.Description
End Sub

Private Sub Http_OnResponseFinished()
    If Done Then Exit Sub
    On Error GoTo Failed
    If OutputFile <> 0 Then Close #OutputFile
    OutputFile = 0
    If Total > 0 And Received <> Total Then Err.Raise 5, , "Файл скачан не полностью"
    Done = True
    Exit Sub
Failed:
    FinishError Err.Description
End Sub

Private Sub Http_OnError(ByVal ErrorNumber As Long, ByVal ErrorDescription As String)
    FinishError "Ошибка соединения: " & ErrorDescription
End Sub

Private Sub Class_Terminate()
    On Error Resume Next
    If Not Done Then Http.Abort
    If OutputFile <> 0 Then Close #OutputFile
End Sub
