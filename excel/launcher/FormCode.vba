Option Explicit

Public Cancelled As Boolean
Private Started As Boolean
Private Busy As Boolean

Private Sub UserForm_Activate()
    If Started Then Exit Sub
    Started = True
    Busy = True
    On Error GoTo Failed
    SetStage "Проверка наличия приложения…"
    DoEvents
    Dim Executable As String
    Executable = FindFSSPApplication()
    If Cancelled Then GoTo CancelledExit
    If Len(Executable) = 0 Then Executable = InstallFSSP(Me)
    If Cancelled Then GoTo CancelledExit
    SetStage "Приложение найдено. Запуск…"
    lblFill.Width = fraProgress.InsideWidth - 8
    lblPercent.Caption = "100%"
    DoEvents
    RunFSSPExecutable Executable
    Busy = False
    Me.Hide
    Exit Sub
CancelledExit:
    Busy = False
    Me.Hide
    Exit Sub
Failed:
    Busy = False
    If Cancelled Then Me.Hide: Exit Sub
    lblStage.Caption = "Не удалось запустить приложение"
    lblDetails.Caption = Err.Description
    cmdCancel.Caption = "Закрыть"
    Me.Repaint
End Sub

Public Sub SetStage(ByVal Message As String)
    lblStage.Caption = Message
    Me.Repaint
End Sub

Public Sub ShowProgress(ByVal Received As Double, ByVal Total As Double, ByVal Seconds As Double)
    Dim Percent As Double, Speed As Double
    If Total > 0 Then
        Percent = Received * 100# / Total
        If Percent > 100# Then Percent = 100#
        lblPercent.Caption = Format$(Percent, "0") & "%"
        lblFill.Width = (fraProgress.InsideWidth - 8) * Percent / 100#
    Else
        lblPercent.Caption = "…"
        lblFill.Width = 0
    End If
    If Seconds > 0 Then Speed = Received / Seconds / 1048576#
    If Total > 0 Then
        lblDetails.Caption = Format$(Received / 1048576#, "0.00") & " МБ из " & Format$(Total / 1048576#, "0.00") & " МБ   ·   " & Format$(Speed, "0.00") & " МБ/с"
    Else
        lblDetails.Caption = "Получено " & Format$(Received / 1048576#, "0.00") & " МБ   ·   " & Format$(Speed, "0.00") & " МБ/с"
    End If
    Me.Repaint
End Sub

Private Sub cmdCancel_Click()
    If Busy Then
        Cancelled = True
        cmdCancel.Enabled = False
        lblStage.Caption = "Отмена…"
    Else
        Me.Hide
    End If
End Sub

Private Sub UserForm_QueryClose(Cancel As Integer, CloseMode As Integer)
    If Busy Then
        Cancel = True
        Cancelled = True
        cmdCancel.Enabled = False
        lblStage.Caption = "Отмена…"
    End If
End Sub
