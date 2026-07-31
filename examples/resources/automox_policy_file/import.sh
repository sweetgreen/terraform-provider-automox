# Policy files cannot be imported. Automox provides no way to read a file's
# contents back, so an imported resource could never populate `content` and
# every subsequent plan would propose replacing the file. Upload it through
# Terraform instead, or delete it in the console first.
