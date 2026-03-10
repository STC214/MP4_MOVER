param(
    [string]$SourceImage,
    [string]$OutputIco
)

$ErrorActionPreference = "Stop"

Add-Type -AssemblyName System.Drawing

if (-not $SourceImage) {
    throw "missing SourceImage"
}

if (-not (Test-Path $SourceImage)) {
    throw "source image not found: $SourceImage"
}

$outputDir = Split-Path -Parent $OutputIco
if ($outputDir) {
    New-Item -ItemType Directory -Force -Path $outputDir | Out-Null
}

$sizes = @(16, 32, 48, 64, 128, 256)
$src = [System.Drawing.Image]::FromFile($SourceImage)

try {
    $pngEntries = New-Object System.Collections.Generic.List[object]

    foreach ($size in $sizes) {
        $bitmap = New-Object System.Drawing.Bitmap($size, $size, [System.Drawing.Imaging.PixelFormat]::Format32bppArgb)
        try {
            $graphics = [System.Drawing.Graphics]::FromImage($bitmap)
            try {
                $graphics.Clear([System.Drawing.Color]::Transparent)
                $graphics.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
                $graphics.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::HighQuality
                $graphics.PixelOffsetMode = [System.Drawing.Drawing2D.PixelOffsetMode]::HighQuality

                $ratio = [Math]::Min($size / $src.Width, $size / $src.Height)
                $drawWidth = [int][Math]::Round($src.Width * $ratio)
                $drawHeight = [int][Math]::Round($src.Height * $ratio)
                $x = [int](($size - $drawWidth) / 2)
                $y = [int](($size - $drawHeight) / 2)
                $graphics.DrawImage($src, $x, $y, $drawWidth, $drawHeight)
            }
            finally {
                $graphics.Dispose()
            }

            $memory = New-Object System.IO.MemoryStream
            try {
                $bitmap.Save($memory, [System.Drawing.Imaging.ImageFormat]::Png)
                $pngEntries.Add([PSCustomObject]@{
                    Size = $size
                    Data = $memory.ToArray()
                }) | Out-Null
            }
            finally {
                $memory.Dispose()
            }
        }
        finally {
            $bitmap.Dispose()
        }
    }

    $file = [System.IO.File]::Open($OutputIco, [System.IO.FileMode]::Create, [System.IO.FileAccess]::Write)
    try {
        $writer = New-Object System.IO.BinaryWriter($file)
        try {
            $writer.Write([UInt16]0)
            $writer.Write([UInt16]1)
            $writer.Write([UInt16]$pngEntries.Count)

            $offset = 6 + ($pngEntries.Count * 16)
            foreach ($entry in $pngEntries) {
                [byte]$byteSize = 0
                if ($entry.Size -lt 256) {
                    $byteSize = [byte]$entry.Size
                }
                $writer.Write($byteSize)
                $writer.Write($byteSize)
                $writer.Write([byte]0)
                $writer.Write([byte]0)
                $writer.Write([UInt16]1)
                $writer.Write([UInt16]32)
                $writer.Write([UInt32]$entry.Data.Length)
                $writer.Write([UInt32]$offset)
                $offset += $entry.Data.Length
            }

            foreach ($entry in $pngEntries) {
                $writer.Write($entry.Data)
            }
        }
        finally {
            $writer.Dispose()
        }
    }
    finally {
        $file.Dispose()
    }
}
finally {
    $src.Dispose()
}
