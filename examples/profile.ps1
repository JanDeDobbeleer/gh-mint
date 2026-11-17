function copilot {
    $gh = Get-Command gh -CommandType Application -ErrorAction Stop | Select-Object -First 1
    & ($gh.Source) mint @args
}
