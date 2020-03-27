
import json
from docutils import nodes
from os.path import isdir, isfile, join, basename, dirname
from os import makedirs, getenv
from shutil import copyfile

##############################################################################
#
# This section determines the behavior of links to local items in .md files.
#
#  if useGitHubURL == True:
#
#     links to local files and directories will be turned into github URLs
#     using either the baseBranch defined here or using the commit SHA.
#
#  if useGitHubURL == False:
#
#     local files will be moved to the website directory structure when built
#     local directories will still be links to github URLs
#
#  if built with GitHub workflows:
#
#     the GitHub URLs will use the commit SHA (GITHUB_SHA environment variable
#     is defined by GitHub workflows) to link to the specific commit. 
#
##############################################################################

baseBranch = "devel"
useGitHubURL = True
commitSHA = getenv('GITHUB_SHA')
githubBaseURL = "https://github.com/intelkevinputnam/pmem-csi/"
githubFileURL = githubBaseURL + "blob/"
githubDirURL = githubBaseURL + "tree/"
if commitSHA:
    githubFileURL = githubFileURL + commitSHA + "/"
    githubDirURL = githubDirURL + commitSHA + "/"
else:
    githubFileURL = githubFileURL + baseBranch + "/"
    githubDirURL = githubDirURL + baseBranch + "/"

# End GitHub URL section

with open('conf.json') as jsonFile:
    conf = json.load(jsonFile)

for item in conf:
    globals()[item] = (conf[item])

def setup(app):
    app.connect('doctree-resolved',fixLocalMDAnchors)
    app.connect('missing-reference',fixRSTLinkInMD)

##############################################################################
#
#  This section defines callbacks that make markdown specific tweaks to
#  either:
#
#  1. Fix something that recommonmark does wrong.
#  2. Provide support for .md files that are written as READMEs in a GitHub
#     repo. 
#
#  Only use these changes if using the extension ``recommonmark``.
#
##############################################################################


# Callback registerd with 'missing-reference'. 
def fixRSTLinkInMD(app, env, node, contnode):
    refTarget = node.get('reftarget')
    filePath = refTarget.lstrip("/")
    if '.rst' in refTarget and "://" not in refTarget:
    # This occurs when a .rst file is referenced from a .md file
    # Currently unable to check if file exists as no file
    # context is provided and links are relative. 
    #
    # Example: [Application examples](examples/readme.rst)
    #
        contnode['refuri'] = contnode['refuri'].replace('.rst','.html')
        contnode['internal'] = "True"
        return contnode
    else:
    # This occurs when a file is referenced for download from an .md file.
    # Construct a list of them and short-circuit the warning. The files 
    # are moved later (need file location context). To avoid warnings,
    # write .md files, make the links absolute. This only marks them fixed
    # if it can verify that they exist.
    #
    # Example: [Makefile](/Makefile)
    #
        if isfile(filePath) or isdir(filePath): 
            return contnode


def normalizePath(docPath,uriPath):
    if uriPath == "":
        return uriPath
    if "#" in uriPath:
    # Strip out anchors
        uriPath = uriPath.split("#")[0]
    if uriPath.startswith("/"):
    # It's an absolute path
        return uriPath.lstrip("/") #path to file from project directory
    else:
    # It's a relative path
        docDir = dirname(docPath)
        return join(docDir,uriPath) #path to file from referencing file


# Callback registerd with 'doctree-resolved'. 
def fixLocalMDAnchors(app, doctree, docname):
    for node in doctree.traverse(nodes.reference):
        uri = node.get('refuri')
        filePath = normalizePath(docname,uri)
        if isfile(filePath):
        # Only do this if the file exists.
        #
        # TODO: Pop a warning if the file doesn't exist. 
        #
            if '.md' in uri and '://' not in uri: 
            # Make sure .md file links that weren't caught are converted.
            # These occur when creating an explicit link to an .md file
            # from an .rst file. By default these are not validated by Sphinx
            # or recommonmark. Only toctree references are validated. recommonmark
            # also fails to convert links to local Markdown files that include
            # anchors. This fixes that as well.
            #
            # Only include this code if .md files are being converted to html
            #
            # Example: `Google Cloud Engine <gce.md>`__
            #          [configuration options](autotest.md#configuration-options)
            #
                node['refuri'] = node['refuri'].replace('.md','.html')
            else: 
            # Handle the case where markdown is referencing local files in the repo
            #
            # Example: [Makefile](/Makefile)
            #
                if useGitHubURL:
                # Replace references to local files with links to the GitHub repo
                #
                    newURI = githubFileURL + filePath
                    print("new url: ", newURI)
                    node['refuri']=newURI
                else:
                # If there are links to local files other than .md (.rst files are caught
                # when warnings are fired), move the files into the Sphinx project, so
                # they can be accessed. 
                    newFileDir = join(app.outdir,dirname(filePath)) # where to move the file in Sphinx output.
                    newFilePath = join(app.outdir,filePath)
                    newURI = uri # if the path is relative no need to change it.
                    if uri.startswith("/"):
                    # It's an absolute path. Need to make it relative.
                        uri = uri.lstrip("/")
                        docDirDepth = len(docname.split("/")) - 1
                        newURI = "../"*docDirDepth + uri
                    if not isdir(newFileDir):
                        makedirs(newFileDir)                
                    copyfile(filePath,newFilePath)
                    node['refuri'] = newURI
        elif "#" not in uri: # ignore anchors
        # turn links to directories into links to the repo
            if isdir(filePath):
                newURI = githubDirURL + filePath
                node['refuri']=newURI
            



