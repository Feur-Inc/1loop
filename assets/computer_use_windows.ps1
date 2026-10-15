$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Console]::InputEncoding = New-Object Text.UTF8Encoding($false)
[Console]::OutputEncoding = New-Object Text.UTF8Encoding($false)
Add-Type -AssemblyName System.Windows.Forms, System.Drawing, UIAutomationClient, UIAutomationTypes
Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
using System.Text;
public static class DesktopNative {
    [StructLayout(LayoutKind.Sequential)] public struct Rect { public int Left, Top, Right, Bottom; }
    [StructLayout(LayoutKind.Sequential)] public struct Mouse { public int X,Y; public uint Data,Flags,Time; public UIntPtr Extra; }
    [StructLayout(LayoutKind.Sequential)] public struct Keyboard { public ushort Key,Scan; public uint Flags,Time; public UIntPtr Extra; }
    [StructLayout(LayoutKind.Explicit)] public struct Payload {
        [FieldOffset(0)] public Mouse Mouse;
        [FieldOffset(0)] public Keyboard Keyboard;
    }
    [StructLayout(LayoutKind.Sequential)] public struct Input { public uint Type; public Payload Payload; }
    public delegate bool EnumWindow(IntPtr window, IntPtr data);
    [DllImport("user32.dll")] public static extern IntPtr SetThreadDpiAwarenessContext(IntPtr value);
    [DllImport("user32.dll")] public static extern bool SetCursorPos(int x,int y);
    [DllImport("user32.dll",SetLastError=true)] static extern uint SendInput(uint count,Input[] data,int size);
    [DllImport("user32.dll")] public static extern bool EnumWindows(EnumWindow callback,IntPtr data);
    [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr window);
    [DllImport("user32.dll",CharSet=CharSet.Unicode)] public static extern int GetWindowText(IntPtr window,StringBuilder text,int count);
    [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr window,out uint process);
    [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr window,out Rect rect);
    [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
    [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr window);
    [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr window,int command);
    [DllImport("user32.dll")] public static extern bool SetWindowPos(IntPtr window,IntPtr after,int x,int y,int width,int height,uint flags);
    [DllImport("user32.dll",SetLastError=true)] public static extern IntPtr OpenInputDesktop(uint flags,bool inherit,uint access);
    [DllImport("user32.dll")] public static extern bool CloseDesktop(IntPtr desktop);
    [DllImport("user32.dll")] static extern IntPtr GetThreadDesktop(uint thread);
    [DllImport("kernel32.dll")] static extern uint GetCurrentThreadId();
    [DllImport("user32.dll",CharSet=CharSet.Unicode)] static extern bool GetUserObjectInformation(IntPtr handle,int index,StringBuilder value,int length,out int needed);
    static string DesktopName(IntPtr handle) {
        var name=new StringBuilder(256);int needed;
        if(!GetUserObjectInformation(handle,2,name,name.Capacity*2,out needed))
            throw new InvalidOperationException("Could not verify the interactive desktop.");
        return name.ToString();
    }
    public static void CheckDesktop() {
        IntPtr desktop=OpenInputDesktop(0,false,0x0101);
        if(desktop==IntPtr.Zero) throw new InvalidOperationException("No interactive desktop. Unlock Windows and handle UAC prompts yourself.");
        try {
            string input=DesktopName(desktop);
            if(input.Equals("Winlogon",StringComparison.OrdinalIgnoreCase)||
               !input.Equals(DesktopName(GetThreadDesktop(GetCurrentThreadId())),StringComparison.OrdinalIgnoreCase))
                throw new InvalidOperationException("Windows is locked or on a secure desktop. Unlock it and handle UAC prompts yourself.");
        } finally { CloseDesktop(desktop); }
    }
    static void Send(Input input) {
        if(SendInput(1,new[]{input},Marshal.SizeOf(typeof(Input)))!=1)
            throw new InvalidOperationException("Windows rejected input. The target may be elevated or on a secure desktop.");
    }
    public static void Key(ushort key,bool up) { var input=new Input {Type=1};input.Payload.Keyboard.Key=key;input.Payload.Keyboard.Flags=(up?2u:0u)|((key>=33&&key<=46)||key==91||key==92?1u:0u);Send(input); }
    public static void Text(string text) {
        foreach(char character in text) {
            var input=new Input {Type=1};input.Payload.Keyboard.Scan=character;input.Payload.Keyboard.Flags=4;Send(input);
            input.Payload.Keyboard.Flags=6;Send(input);
        }
    }
    public static void MouseEvent(uint flags,int data) {
        var input=new Input {Type=0};input.Payload.Mouse.Flags=flags;input.Payload.Mouse.Data=unchecked((uint)data);Send(input);
    }
}
'@
# UIA rectangles and captured pixels must use the same coordinate space at
# every Windows display scale, including desktops with negative origins.
[void][DesktopNative]::SetThreadDpiAwarenessContext([IntPtr](-4))
$script:sharing = $false
$script:screen = $null
$script:seenScreen = $false
$script:elements = @()

function WindowInfo([IntPtr]$handle) {
    $title = New-Object Text.StringBuilder 1024
    [void][DesktopNative]::GetWindowText($handle,$title,1024)
    [uint32]$processId = 0
    [void][DesktopNative]::GetWindowThreadProcessId($handle,[ref]$processId)
    $bounds = New-Object DesktopNative+Rect
    [void][DesktopNative]::GetWindowRect($handle,[ref]$bounds)
    return @{window_id=$handle.ToInt64().ToString();title=$title.ToString();pid=$processId;x=$bounds.Left;y=$bounds.Top;width=($bounds.Right-$bounds.Left);height=($bounds.Bottom-$bounds.Top)}
}
function Windows {
    $script:windows = New-Object Collections.Generic.List[object]
    $callback = [DesktopNative+EnumWindow]{param($handle,$unused)
        if ([DesktopNative]::IsWindowVisible($handle)) {
            $item = WindowInfo $handle
            if ($item.title) { $script:windows.Add($item) }
        }
        return $true
    }
    [void][DesktopNative]::EnumWindows($callback,[IntPtr]::Zero)
    return $script:windows.ToArray()
}
function TargetWindow($arguments) {
    $all = @(Windows)
    if ($arguments.window_id) { $all = @($all | Where-Object {$_.window_id -eq [string]$arguments.window_id}) }
    elseif ($arguments.title) { $all = @($all | Where-Object {$_.title -eq [string]$arguments.title}) }
    elseif ($arguments.pid) { $all = @($all | Where-Object {$_.pid -eq [int]$arguments.pid}) }
    elseif ($arguments.app_name_or_bundle_identifier) {
        $name = [string]$arguments.app_name_or_bundle_identifier
        $all = @($all | Where-Object { $_.title -like "*$name*" -or (Get-Process -Id $_.pid -ErrorAction SilentlyContinue).ProcessName -eq $name })
    } else { return [DesktopNative]::GetForegroundWindow() }
    if ($all.Count -ne 1) { throw 'Window selector must match exactly one visible window.' }
    return [IntPtr]([long]$all[0].window_id)
}
function Activate($arguments) {
    $handle = TargetWindow $arguments
    [void][DesktopNative]::ShowWindow($handle,9)
    [void][DesktopNative]::SetForegroundWindow($handle)
    Start-Sleep -Milliseconds 120
    if ([DesktopNative]::GetForegroundWindow() -ne $handle) { throw 'Could not verify target focus. Ask the user to activate the window.' }
    return @{ok=$true;window=(WindowInfo $handle)}
}
function StartSharing($arguments) {
    [DesktopNative]::CheckDesktop()
    if ($script:sharing) { return @{sharing=$true} }
    $screens = @([Windows.Forms.Screen]::AllScreens)
    $selected = -1
    $remember = $false
    if ($arguments.restore_token -eq 'windows:desktop') { $selected = $screens.Count; $remember = $true }
    for ($index=0; $index -lt $screens.Count; $index++) {
        if ($arguments.restore_token -eq ('windows:'+$screens[$index].DeviceName)) { $selected=$index; $remember=$true }
    }
    if ($selected -lt 0) {
        $form = New-Object Windows.Forms.Form
        $form.Text='1loop - Allow Computer Use'; $form.Width=520; $form.Height=270
        $form.StartPosition='CenterScreen'; $form.TopMost=$true; $form.FormBorderStyle='FixedDialog'; $form.MaximizeBox=$false
        $label=New-Object Windows.Forms.Label; $label.SetBounds(20,15,465,60)
        $label.Text='Allow the agent to capture the selected screen and control your mouse and keyboard? You can revoke this in 1loop Settings.'
        $choice=New-Object Windows.Forms.ComboBox; $choice.SetBounds(20,80,460,30); $choice.DropDownStyle='DropDownList'
        foreach($monitor in $screens) { [void]$choice.Items.Add(($monitor.DeviceName+' - '+$monitor.Bounds.Width+' x '+$monitor.Bounds.Height)) }
        [void]$choice.Items.Add('Whole desktop'); $choice.SelectedIndex=0
        $check=New-Object Windows.Forms.CheckBox; $check.SetBounds(20,120,450,25); $check.Text='Remember this permission'
        $allow=New-Object Windows.Forms.Button; $allow.SetBounds(285,165,90,32); $allow.Text='Allow'; $allow.DialogResult='OK'
        $deny=New-Object Windows.Forms.Button; $deny.SetBounds(390,165,90,32); $deny.Text='Deny'; $deny.DialogResult='Cancel'
        $form.Controls.AddRange(@($label,$choice,$check,$allow,$deny)); $form.AcceptButton=$allow; $form.CancelButton=$deny
        $decision=$form.ShowDialog(); $selected=$choice.SelectedIndex; $remember=$check.Checked; $form.Dispose()
        if ($decision -ne [Windows.Forms.DialogResult]::OK) { throw 'Computer Use permission denied.' }
    }
    $token='windows:desktop'; $bounds=[Windows.Forms.SystemInformation]::VirtualScreen
    if ($selected -lt $screens.Count) { $bounds=$screens[$selected].Bounds; $token='windows:'+$screens[$selected].DeviceName }
    $script:screen=@{width=$bounds.Width;height=$bounds.Height;x=$bounds.X;y=$bounds.Y}
    $script:sharing=$true; $script:seenScreen=$false
    return @{started=$true;screen=$script:screen;restore_token=$(if($remember){$token}else{''})}
}
function Point($x,$y) {
    if (-not $script:seenScreen) { throw 'Inspect a fresh screenshot before coordinate input.' }
    if ($x -lt 0 -or $y -lt 0 -or $x -ge $script:screen.width -or $y -ge $script:screen.height) { throw 'Coordinates are outside the shared screen.' }
    if (-not [DesktopNative]::SetCursorPos([int]($x+$script:screen.x),[int]($y+$script:screen.y))) { throw 'Could not move the pointer.' }
}
function Screenshot {
    $bitmap=New-Object Drawing.Bitmap $script:screen.width,$script:screen.height
    $graphics=[Drawing.Graphics]::FromImage($bitmap)
    $stream=New-Object IO.MemoryStream
    try {
        $graphics.CopyFromScreen($script:screen.x,$script:screen.y,0,0,$bitmap.Size)
        $bitmap.Save($stream,[Drawing.Imaging.ImageFormat]::Png)
        $script:seenScreen=$true
        return @(@{type='text';text=(@{screen=$script:screen;scale_x=1;scale_y=1;coordinate_width=$script:screen.width;coordinate_height=$script:screen.height}|ConvertTo-Json -Compress -Depth 8)},@{type='image';mimeType='image/png';data=[Convert]::ToBase64String($stream.ToArray())})
    } finally { $graphics.Dispose();$bitmap.Dispose();$stream.Dispose() }
}
function PressKey([string]$text) {
    $names=@{Ctrl=17;Control=17;Alt=18;Shift=16;Super=91;Win=91;Meta=91;Enter=13;Return=13;Tab=9;Escape=27;Esc=27;Backspace=8;Delete=46;Insert=45;Home=36;End=35;PageUp=33;PageDown=34;ArrowLeft=37;ArrowUp=38;ArrowRight=39;ArrowDown=40;Space=32}
    $keys=New-Object Collections.Generic.List[uint16]
    foreach($part in $text.Split('+')) {
        if ($names.ContainsKey($part)) { $keys.Add([uint16]$names[$part]) }
        elseif ($part -match '^F([1-9]|1[0-9]|2[0-4])$') { $keys.Add([uint16](111+[int]$Matches[1])) }
        elseif ($part.Length -eq 1 -and $part -match '^[a-zA-Z0-9]$') { $keys.Add([uint16][char]$part.ToUpperInvariant()) }
        else { throw ('Unknown key: '+$part) }
    }
    $held=New-Object Collections.Generic.List[uint16]
    try { foreach($key in $keys) { [DesktopNative]::Key($key,$false);$held.Add($key) } }
    finally { for($index=$held.Count-1;$index -ge 0;$index--) { [DesktopNative]::Key($held[$index],$true) } }
}
function AppState($arguments) {
    $root=[Windows.Automation.AutomationElement]::FromHandle((TargetWindow $arguments))
    $limit=500; if($arguments.max_nodes) { $limit=[Math]::Max(1,[Math]::Min(2000,[int]$arguments.max_nodes)) }
    $queue=New-Object Collections.Generic.Queue[object];$queue.Enqueue($root)
    $nodes=New-Object Collections.Generic.List[object];$script:elements=New-Object Collections.Generic.List[object]
    $walker=[Windows.Automation.TreeWalker]::ControlViewWalker
    while($queue.Count -gt 0 -and $nodes.Count -lt $limit) {
        $element=$queue.Dequeue()
        try {
            $current=$element.Current; $bounds=$current.BoundingRectangle
            $index=$script:elements.Count; $script:elements.Add($element)
            $rectangle=$null;if(-not $bounds.IsEmpty){$rectangle=@{x=$bounds.X;y=$bounds.Y;width=$bounds.Width;height=$bounds.Height}}
            $nodes.Add(@{element_index=$index;name=$current.Name;role=$current.ControlType.ProgrammaticName;automation_id=$current.AutomationId;enabled=$current.IsEnabled;bounds=$rectangle})
            $child=$walker.GetFirstChild($element)
            while($null -ne $child -and $queue.Count -lt $limit) { $queue.Enqueue($child);$child=$walker.GetNextSibling($child) }
        } catch [Windows.Automation.ElementNotAvailableException] { }
    }
    return @{window=(WindowInfo ([IntPtr]$root.Current.NativeWindowHandle));elements=$nodes.ToArray();truncated=($queue.Count -gt 0)}
}
function Element($arguments) {
    if ($script:elements.Count -eq 0) { throw 'Read get_app_state before using accessibility selectors.' }
    if ($null -ne $arguments.element_index) {
        $index=[int]$arguments.element_index
        if ($index -lt 0 -or $index -ge $script:elements.Count) { throw 'Invalid element index.' }
        return $script:elements[$index]
    }
    $matches=@($script:elements | Where-Object {
        $current=$_.Current
        ($null -eq $arguments.name -or $current.Name -eq $arguments.name) -and
        ($null -eq $arguments.role -or $current.ControlType.ProgrammaticName -eq ('ControlType.'+$arguments.role) -or $current.ControlType.ProgrammaticName -eq $arguments.role)
    })
    if($matches.Count -ne 1) { throw 'Accessibility selector must match exactly one element.' }
    return $matches[0]
}
function Schema {
    $definitions=@{
        get_app_state=@{app_name_or_bundle_identifier='string';window_id='string';title='string';max_nodes='integer'}
        list_windows=@{};focused_window=@{};activate_window=@{window_id='string';title='string'}
        perform_action=@{element_index='integer';name='string';role='string';action='string'}
        set_value=@{element_index='integer';name='string';role='string';value='string'}
        move_window=@{window_id='string';title='string';x='integer';y='integer'}
        resize_window=@{window_id='string';title='string';width='integer';height='integer'}
        doctor=@{};setup_accessibility=@{}
    }
    $tools=@(foreach($name in $definitions.Keys) {
        $properties=@{};foreach($field in $definitions[$name].Keys){$properties[$field]=@{type=$definitions[$name][$field]}}
        @{name=$name;description=$name;inputSchema=@{type='object';properties=$properties}}
    })
    return @{tools=$tools}
}
function Action([string]$name,$arguments) {
    if ($name -eq 'start') { return StartSharing $arguments }
    if ($name -eq 'stop') { $script:sharing=$false;$script:seenScreen=$false;$script:elements=@();return @{sharing=$false} }
    if (-not $script:sharing) { throw 'Approve Computer Use sharing first.' }
    [DesktopNative]::CheckDesktop()
    switch($name) {
        status { return @{sharing=$true;screen=$script:screen} }
        screenshot { return Screenshot }
        list_windows { return @{windows=@(Windows)} }
        focused_window { return WindowInfo ([DesktopNative]::GetForegroundWindow()) }
        activate_window { return Activate $arguments }
        get_app_state { return AppState $arguments }
        doctor { return @{available=$true;backend='Windows UI Automation and SendInput';screen=$script:screen} }
        setup_accessibility { return @{available=$true;message='Windows UI Automation requires no extension. Elevated and secure windows remain protected.'} }
        move { Point $arguments.x $arguments.y;return @{ok=$true} }
        click {
            Point $arguments.x $arguments.y
            $down=2;$up=4;if($arguments.button -eq 'right'){$down=8;$up=16};if($arguments.button -eq 'middle'){$down=32;$up=64}
            $count=1;if($arguments.click_count){$count=[Math]::Max(1,[Math]::Min(3,[int]$arguments.click_count))}
            for($index=0;$index -lt $count;$index++){[DesktopNative]::MouseEvent($down,0);[DesktopNative]::MouseEvent($up,0);Start-Sleep -Milliseconds 50}
            return @{ok=$true}
        }
        drag {
            Point $arguments.start_x $arguments.start_y
            [DesktopNative]::MouseEvent(2,0)
            try { for($step=1;$step -le 20;$step++){Point ($arguments.start_x+($arguments.end_x-$arguments.start_x)*$step/20) ($arguments.start_y+($arguments.end_y-$arguments.start_y)*$step/20);Start-Sleep -Milliseconds 10} }
            finally { [DesktopNative]::MouseEvent(4,0) }
            return @{ok=$true}
        }
        scroll {
            if($null -ne $arguments.x -and $null -ne $arguments.y){Point $arguments.x $arguments.y}
            $pages=1;if($arguments.pages){$pages=[Math]::Max(0.1,[Math]::Min(20,[double]$arguments.pages))}
            $delta=[int](120*3*$pages);$flag=0x800
            switch($arguments.direction){up{};down{$delta=-$delta};left{$flag=0x1000;$delta=-$delta};right{$flag=0x1000};default{throw 'Unknown scroll direction.'}}
            [DesktopNative]::MouseEvent($flag,$delta);return @{ok=$true}
        }
        type_text { [DesktopNative]::Text([string]$arguments.text);Start-Sleep -Milliseconds 150;return @{ok=$true} }
        press_key { PressKey $arguments.key;Start-Sleep -Milliseconds 100;return @{ok=$true} }
        perform_action {
            $element=Element $arguments
            $action='invoke';if($arguments.action){$action=$arguments.action}
            switch($action){
                {$_ -in '', 'invoke','click','press'} { ([Windows.Automation.InvokePattern]$element.GetCurrentPattern([Windows.Automation.InvokePattern]::Pattern)).Invoke() }
                focus {$element.SetFocus()}
                toggle { ([Windows.Automation.TogglePattern]$element.GetCurrentPattern([Windows.Automation.TogglePattern]::Pattern)).Toggle() }
                select { ([Windows.Automation.SelectionItemPattern]$element.GetCurrentPattern([Windows.Automation.SelectionItemPattern]::Pattern)).Select() }
                expand { ([Windows.Automation.ExpandCollapsePattern]$element.GetCurrentPattern([Windows.Automation.ExpandCollapsePattern]::Pattern)).Expand() }
                collapse { ([Windows.Automation.ExpandCollapsePattern]$element.GetCurrentPattern([Windows.Automation.ExpandCollapsePattern]::Pattern)).Collapse() }
                default { throw 'Unsupported accessibility action. Use invoke, focus, toggle, select, expand or collapse.' }
            };return @{ok=$true}
        }
        set_value { $element=Element $arguments;([Windows.Automation.ValuePattern]$element.GetCurrentPattern([Windows.Automation.ValuePattern]::Pattern)).SetValue([string]$arguments.value);return @{ok=$true} }
        {$_ -in 'move_window','resize_window'} {
            $handle=TargetWindow $arguments;$bounds=WindowInfo $handle
            $x=$bounds.x;$y=$bounds.y;$width=$bounds.width;$height=$bounds.height
            if($name -eq 'move_window'){$x=[int]$arguments.x;$y=[int]$arguments.y}else{$width=[int]$arguments.width;$height=[int]$arguments.height;if($width -lt 1 -or $height -lt 1){throw 'Window size must be positive.'}}
            if(-not [DesktopNative]::SetWindowPos($handle,[IntPtr]::Zero,$x,$y,$width,$height,0x14)){throw 'Could not change window bounds.'};return @{ok=$true}
        }
        default { throw ('Unknown Computer Use action: '+$name) }
    }
}
while($null -ne ($line=[Console]::ReadLine())) {
    $request=$null
    try {
        $request=$line|ConvertFrom-Json
        if($null -eq $request.id){continue}
        switch($request.method) {
            initialize { $result=@{protocolVersion='2025-03-26';capabilities=@{tools=@{}};serverInfo=@{name='1loop-windows-computer';version='1'}} }
            'tools/list' { $result=Schema }
            'tools/call' {
                $value=Action $request.params.name $request.params.arguments
                if($request.params.name -eq 'screenshot'){$content=@($value)}else{$content=@(@{type='text';text=($value|ConvertTo-Json -Compress -Depth 16)})}
                $result=@{content=$content}
            }
            default { throw 'Unknown protocol method.' }
        }
        $response=@{jsonrpc='2.0';id=$request.id;result=$result}
    } catch { $response=@{jsonrpc='2.0';id=$request.id;error=@{code=-32000;message=$_.Exception.Message}} }
    [Console]::WriteLine(($response|ConvertTo-Json -Compress -Depth 24))
}
